package main

import (
	"context"
	"fmt"
	"io/ioutil"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/ieee0824/virtual-neighbor-proxy/config"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

var defaultConfig = config.NewClientConfig()

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

func ws(w http.ResponseWriter, r *http.Request) error {
	connectionID := uuid.New()

	fc, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		return err
	}

	conn, err := grpc.Dial(defaultConfig.RelayServerConfig.Addr(), grpc.WithInsecure())
	if err != nil {
		return err
	}
	defer conn.Close()

	client := remote.NewProxyClient(conn)

	// 裏側でのweb socket connectionを確立する
	connectionResult, err := client.WebSocketFrontConnecter(context.Background(), &remote.WebSocketConnecterRequest{
		ConnectionId: connectionID.String(),
	})
	if err != nil {
		return err
	}
	if connectionResult.ConnectionId != connectionID.String() {
		return fmt.Errorf("connection id is not match: %s, %s", connectionID.String(), connectionResult.ConnectionId)
	}

	if connectionResult.Status != "Success" {
		return fmt.Errorf("connection failed")
	}

	stream, err := client.WebSocketFrontend(context.Background())
	if err != nil {
		return err
	}
	var wg sync.WaitGroup

	// WebSocketからもらってgRPCに投げるしょり
	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		for {
			msgType, msg, err := fc.ReadMessage()
			if err != nil {
				log.Error().Err(err).Msg("")
				break
			}

			wsPacket := &remote.WebSocketPacket{
				ConnectionId: connectionID.String(),
				MessageType:  int32(msgType),
				Data:         msg,
				MessageId:    uuid.New().String(),
			}

			if err := stream.Send(wsPacket); err != nil {
				log.Error().Err(err).Msg("")
				break
			}
		}
	}(&wg)

	// gRPCからもらってWebSocketに投げる処理
	wg.Add(1)
	go func(wg *sync.WaitGroup) {
		defer wg.Done()
		for {
			wsPacket, err := stream.Recv()
			if err != nil {
				log.Error().Err(err).Msg("")
				break
			}
			if err := fc.WriteMessage(int(wsPacket.MessageType), wsPacket.Data); err != nil {
				log.Error().Err(err).Msg("")
				break
			}
		}

	}(&wg)

	wg.Wait()

	return nil
}

func main() {
	log.Logger = log.With().Caller().Logger()
	log.Info().Msg("start")

	r := gin.Default()

	r.Any("*all", func(ctx *gin.Context) {
		defer ctx.Request.Body.Close()
		// websocketを無視
		if h := ctx.Request.Header.Get("Upgrade"); h == "websocket" {
			ws(ctx.Writer, ctx.Request)
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
