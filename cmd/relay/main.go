package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"

	"github.com/ieee0824/virtual-neighbor-proxy/config"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

type ConnectionID string
type Domain string

func (c ConnectionID) String() string {
	return string(c)
}

type RequestQueue struct {
	c chan *remote.HttpRequestWrapper
}

type ResponseQueue struct {
	c chan *remote.HttpResponseWrapper
}

var requestQuenes = map[Domain]*RequestQueue{}
var responseQueues = map[ConnectionID]*ResponseQueue{}

type RelayServer struct {
	remote.ProxyServer
}

var wsRequests = make(chan *remote.WebSocketConnecterRequest)
var wsResponse = map[string]chan *remote.WebSocketConnecterResponse{}

// frontからきたwebsocketコネクションをbackendにつなぐ
func (s *RelayServer) WebSocketFrontConnecter(ctx context.Context, req *remote.WebSocketConnecterRequest) (*remote.WebSocketConnecterResponse, error) {
	wsResponse[req.ConnectionId] = make(chan *remote.WebSocketConnecterResponse)
	wsRequests <- req

	return <-wsResponse[req.ConnectionId], nil
}

func (s *RelayServer) WebSocketBackendConnecterReceive(_ *remote.Null, stream remote.Proxy_WebSocketBackendConnecterReceiveServer) error {
	for {
		req, ok := <-wsRequests
		if !ok {
			return errors.New("close wsReq channel")
		}
		if err := stream.Send(req); err != nil {
			return err
		}
	}
}

func (s *RelayServer) WebSocketBackendConnecterSend(ctx context.Context, resp *remote.WebSocketConnecterResponse) (*remote.Null, error) {
	wsResponse[resp.ConnectionId] <- resp
	return nil, nil
}

var wsToBack = make(chan *remote.WebSocketPacket)
var wsToFront = make(chan *remote.WebSocketPacket)

func (s *RelayServer) WebSocketFrontend(stream remote.Proxy_WebSocketFrontendServer) (err error) {
	go func() {
		for {
			if err = stream.Send(<-wsToFront); err != nil {
				return
			}
		}
	}()
	for {
		frontPacket, err := stream.Recv()
		if err != nil {
			return err
		}
		wsToBack <- frontPacket
	}
}

func (s *RelayServer) WebSocketBackend(stream remote.Proxy_WebSocketBackendServer) (err error) {
	go func() {
		for {
			if err = stream.Send(<-wsToBack); err != nil {
				return
			}
		}
	}()

	for {
		backPacket, err := stream.Recv()
		if err != nil {
			return err
		}
		wsToFront <- backPacket
	}
}

// frontからのリクエストを受ける
// コネクションを作る
// backendからのリクエストをrequest queue経由でフロントに返す
func (s *RelayServer) FrontendEndpoint(ctx context.Context, request *remote.HttpRequestWrapper) (*remote.HttpResponseWrapper, error) {
	responseQueues[ConnectionID(request.ConnectionId)] = &ResponseQueue{
		c: make(chan *remote.HttpResponseWrapper),
	}

	requestQueue, ok := requestQuenes[Domain(request.Domain)]
	if !ok {
		return nil, errors.New("Error: request queue doen not exist")
	}

	requestQueue.c <- request

	responseQueue := responseQueues[ConnectionID(request.ConnectionId)]
	response := <-responseQueue.c
	close(responseQueue.c)
	delete(responseQueues, ConnectionID(request.ConnectionId))

	return response, nil
}

// はじめにNATに穴を開ける
// フロントからのリクエストをバックエンドに流す
func (s *RelayServer) BackendReceive(con *remote.Connection, stream remote.Proxy_BackendReceiveServer) error {
	log.Info().Msgf("%s is connected", con.DeveloperName)
	requestQueue := &RequestQueue{
		c: make(chan *remote.HttpRequestWrapper),
	}
	requestQuenes[Domain(con.Domain)] = requestQueue
	for {
		if err := stream.Send(<-requestQueue.c); err != nil {
			return err
		}
	}
}

// バックエンドからのリクエストを受け取る
func (s *RelayServer) BackendSend(stream remote.Proxy_BackendSendServer) error {
	for {
		response, err := stream.Recv()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}

		if _, ok := responseQueues[ConnectionID(response.ConnectionId)]; !ok {
			return errors.New("response queue does not exist")
		}

		responseQueues[ConnectionID(response.ConnectionId)].c <- response
	}
}

var defaultConfig = config.NewRelayServerConfig()

func main() {
	log.Logger = log.With().Caller().Logger()
	log.Info().Msg("start")

	con, err := net.Listen("tcp", fmt.Sprintf(":%s", defaultConfig.Port))
	if err != nil {
		log.Fatal().Err(err).Msg("")
	}

	s := grpc.NewServer()
	var server RelayServer

	remote.RegisterProxyServer(s, &server)
	if err := s.Serve(con); err != nil {
		log.Fatal().Err(err).Msg("")
	}
}
