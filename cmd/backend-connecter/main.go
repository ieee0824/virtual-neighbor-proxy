package main

import (
	"bytes"
	"context"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"

	"github.com/ieee0824/virtual-neighbor-proxy/config"
	"github.com/ieee0824/virtual-neighbor-proxy/remote"
	"github.com/rs/zerolog/log"
	"google.golang.org/grpc"
)

var defaultConfig = config.NewBackendConnecterConfig()

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

	if err := connect(client, &remote.Connection{
		Domain:        defaultConfig.BackendHostName,
		DeveloperName: defaultConfig.DeveloperName,
	}); err != nil {
		log.Fatal().Err(err).Msg("")
	}
}
