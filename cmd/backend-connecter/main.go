package main

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"
	"github.com/ieee0824/virtual-neighbor-proxy/config"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

var defaultConfig = config.NewBackendConnecterConfig()

var wsConnection = map[string]*websocket.Conn{}

func wsConnect(client remote.ProxyClient, wg *sync.WaitGroup) error {
	defer wg.Done()
	stream, err := client.WebSocketBackendConnecterReceive(context.Background(), &remote.Null{})
	if err != nil {
		return err
	}

	for {
		wsConnectReq, err := stream.Recv()
		if err != nil {
			return err
		}

		go func(wsConnectReq *remote.WebSocketConnecterRequest) {
			u, err := url.Parse(wsConnectReq.GetHttpRequestURL())
			if err != nil {
				log.Error().Err(err).Msg("")
				return
			}

			if defaultConfig.Scheme == "http" {
				u.Scheme = "ws"
			} else {
				u.Scheme = "wss"
			}

			u.Host = defaultConfig.BackendHostName

			conn, _, err := websocket.DefaultDialer.Dial(u.String(), nil)
			if err != nil {
				log.Error().Err(err).Msg("")
				return
			}

			wsConnection[wsConnectReq.GetConnectionId()] = conn
			if _, err := client.WebSocketBackendConnecterSend(context.Background(), &remote.WebSocketConnecterResponse{
				ConnectionId: wsConnectReq.GetConnectionId(),
				Status:       "Success",
			}); err != nil {
				conn.Close()
				delete(wsConnection, wsConnectReq.GetConnectionId())
				log.Error().Err(err).Msg("")
				return
			}

			// wsから受け取って送信する
			for {
				msgType, msg, err := conn.ReadMessage()
				if err != nil {
					log.Error().Err(err).Msg("")
					return
				}
				stream, err := client.WebSocketBackend(context.Background())
				if err != nil {
					log.Error().Err(err).Msg("")
					return
				}
				if err := stream.Send(&remote.WebSocketPacket{
					ConnectionId: wsConnectReq.ConnectionId,
					Data:         msg,
					MessageType:  int32(msgType),
				}); err != nil {
					log.Error().Err(err).Msg("")
					return
				}
			}
		}(wsConnectReq)
	}
}

func wsWait(client remote.ProxyClient, wg *sync.WaitGroup) error {
	defer wg.Done()

	stream, err := client.WebSocketBackend(context.Background())
	if err != nil {
		return err
	}

	// gRPCからうけとって送信する
	for {
		wsPacket, err := stream.Recv()
		if err != nil {
			log.Error().Err(err).Msg("")
			continue
		}
		go func() {
			wsConn, ok := wsConnection[wsPacket.GetConnectionId()]
			if !ok {
				log.Error().Err(errors.New("not fined ws connection")).Msg("")
				return
			}

			if err := wsConn.WriteMessage(int(wsPacket.GetMessageType()), wsPacket.GetData()); err != nil {
				log.Error().Err(err).Msg("")
				return
			}

		}()
	}
}

func connect(client remote.ProxyClient, connectionOpts *remote.Connection) error {
	stream, err := client.BackendReceive(context.Background(), connectionOpts)
	if err != nil {
		return err
	}

	for {
		reqWrapper, err := stream.Recv()
		if err == io.EOF {
			return nil
		}

		headers := http.Header{}
		for _, h := range reqWrapper.GetHeaders() {
			for _, v := range h.Value {
				headers.Set(h.Key, v)
			}
		}

		log.Debug().
			Str("method", reqWrapper.GetHttpMethod()).
			Str("url", reqWrapper.GetHttpRequestURL()).
			Str("connection_id", reqWrapper.GetConnectionId()).
			Str("domain", reqWrapper.GetDomain()).
			Interface("header", headers).
			Int("body_length", len(reqWrapper.GetBody())).
			Msg("receive request")

		u, err := url.Parse(reqWrapper.GetHttpRequestURL())
		if err != nil {
			return err
		}

		u.Host = defaultConfig.BackendHostName
		u.Scheme = defaultConfig.Scheme

		var req *http.Request
		if reqWrapper.GetHttpMethod() == http.MethodGet {
			r, err := http.NewRequest(
				reqWrapper.GetHttpMethod(),
				u.String(),
				nil,
			)
			if err != nil {
				return err
			}
			req = r
		} else {
			r, err := http.NewRequest(
				reqWrapper.GetHttpMethod(),
				u.String(),
				bytes.NewBuffer(reqWrapper.GetBody()),
			)
			if err != nil {
				return err
			}
			req = r
		}

		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		body, err := ioutil.ReadAll(resp.Body)
		if err != nil {
			return err
		}
		resp.Body.Close()

		respWrapper := &remote.HttpResponseWrapper{
			ConnectionId: reqWrapper.ConnectionId,
			Status:       int32(resp.StatusCode),
			Body:         body,
			Headers:      map[string]*remote.HttpHeader{},
		}

		for key, v := range resp.Header {
			respWrapper.Headers[key] = &remote.HttpHeader{
				Key:   key,
				Value: v,
			}
		}

		sendStream, err := client.BackendSend(context.Background())
		if err != nil {
			return err
		}
		if err := sendStream.Send(respWrapper); err != nil {
			return err
		}
	}
}

func main() {
	log.Logger = log.With().Caller().Logger()
	log.Info().Msg("start")
	conn, err := grpc.Dial(defaultConfig.RelayServerConfig.Addr(), grpc.WithInsecure(), grpc.WithBlock())
	if err != nil {
		log.Fatal().Err(err).Msg("")
	}
	defer conn.Close()

	client := remote.NewProxyClient(conn)

	var wg sync.WaitGroup
	wg.Add(1)
	go wsConnect(client, &wg)
	wg.Add(1)
	go wsWait(client, &wg)

	if err := connect(client, &remote.Connection{
		Domain:        defaultConfig.BackendHostName,
		DeveloperName: defaultConfig.DeveloperName,
	}); err != nil {
		log.Fatal().Err(err).Msg("")
	}
	wg.Wait()
}
