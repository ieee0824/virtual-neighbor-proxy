package main

import (
	"context"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ieee0824/virtual-neighbor-proxy/config"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

var defaultConfig = config.NewClientConfig()

func main() {
	log.Logger = log.With().Caller().Logger()
	log.Info().Msg("start")

	r := gin.Default()

	r.Any("*all", func(ctx *gin.Context) {
		defer ctx.Request.Body.Close()
		// websocketを無視
		if h := ctx.Request.Header.Get("Upgrade"); h != "" {
			ctx.JSON(http.StatusHTTPVersionNotSupported, nil)
			ctx.Abort()
			return
		}
		conn, err := grpc.Dial(defaultConfig.RelayServerConfig.Addr(), grpc.WithInsecure())
		if err != nil {
			log.Fatal().Err(err).Msg("")
		}

		defer conn.Close()
		u := ctx.Request.URL
		u.Host = ctx.Request.Host
		if defaultConfig.EnableTLS {
			u.Scheme = "https"
		} else {
			u.Scheme = "http"
		}

		body := []byte{}
		if ctx.Request.Method != http.MethodGet {
			b, err := ioutil.ReadAll(ctx.Request.Body)
			if err != nil {
				log.Fatal().Err(err).Msg("")
			}
			body = b
		}

		connectionID := uuid.New().String()
		log.Debug().
			Str("connection_id", connectionID).
			Str("http_method", ctx.Request.Method).
			Msg("")
		client := remote.NewProxyClient(conn)
		message := &remote.HttpRequestWrapper{
			HttpMethod:     ctx.Request.Method,
			HttpRequestURL: u.String(),
			Body:           body,
			ConnectionId:   connectionID,
			Domain:         ctx.Request.Host,
			Headers:        map[string]*remote.HttpHeader{},
		}

		for k, vs := range ctx.Request.Header {
			message.Headers[k] = &remote.HttpHeader{
				Key:   k,
				Value: vs,
			}

		}

		resp, err := client.FrontendEndpoint(context.TODO(), message)
		if err != nil {
			log.Error().Err(err).Msg("")
			ctx.JSON(http.StatusInternalServerError, nil)
			return
		}

		for key, header := range resp.GetHeaders() {
			for _, v := range header.Value {
				ctx.Header(key, v)
			}
		}

		ctx.Status(int(resp.GetStatus()))
		ctx.Writer.Write(resp.GetBody())
	})

	if defaultConfig.EnableTLS {
		if err := r.RunTLS(
			defaultConfig.Addr(),
			defaultConfig.SslCertFileName,
			defaultConfig.SslCertKeyFileName,
		); err != nil {
			log.Fatal().Err(err).Msg("")
		}
	} else {
		if err := r.Run(defaultConfig.Addr()); err != nil {
			log.Fatal().Err(err).Msg("")
		}
	}
}
