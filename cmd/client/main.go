package main

import (
	"context"
	"io/ioutil"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

func main() {
	log.Logger = log.With().Caller().Logger()
	log.Info().Msg("start")

	r := gin.Default()

	r.Any("*all", func(ctx *gin.Context) {
		defer ctx.Request.Body.Close()
		conn, err := grpc.Dial("127.0.0.1:20000", grpc.WithInsecure())
		if err != nil {
			log.Fatal().Err(err).Msg("")
		}

		defer conn.Close()
		u := ctx.Request.URL
		u.Host = "localhost:8081"
		u.Scheme = "http"

		body := []byte{}
		if ctx.Request.Method != http.MethodGet {
			b, err := ioutil.ReadAll(ctx.Request.Body)
			if err != nil {
				log.Fatal().Err(err).Msg("")
			}
			body = b
		}

		connectionID := uuid.New().String()
		log.Debug().Str("connection_id", connectionID).Msg("")
		client := remote.NewProxyClient(conn)
		message := &remote.HttpRequestWrapper{
			HttpMethod:     ctx.Request.Method,
			HttpRequestURL: u.String(),
			Body:           body,
			ConnectionId:   connectionID,
			Domain:         "localhost:8081",
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

	r.Run(":8080")
}
