package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"time"

	"github.com/chenyanchen/resorch"
	"github.com/chenyanchen/resorch/exp/reload"
)

type httpServerOpt struct {
	Port    int    `json:"port"`
	Message string `json:"message"`
}

type runningHTTPServer struct {
	server   *http.Server
	listener net.Listener
}

func main() {
	reg := resorch.NewRegistry()
	resorch.MustRegister(reg, "http-server", "std", resorch.Definition[httpServerOpt, *runningHTTPServer]{
		Build: func(_ context.Context, _ resorch.Resolver, opt httpServerOpt) (*runningHTTPServer, error) {
			addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(opt.Port))
			listener, err := net.Listen("tcp", addr)
			if err != nil {
				return nil, err
			}
			server := &http.Server{
				Addr: addr,
				Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					_, _ = io.WriteString(w, opt.Message)
				}),
			}
			go func() {
				_ = server.Serve(listener)
			}()
			return &runningHTTPServer{server: server, listener: listener}, nil
		},
		Close: func(ctx context.Context, srv *runningHTTPServer) error {
			shutdownCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
			defer cancel()
			if err := srv.server.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return err
			}
			return nil
		},
	})

	port1 := pickFreePort()
	port2 := pickFreePort()
	for port2 == port1 {
		port2 = pickFreePort()
	}

	initialSpecs := []resorch.NodeSpec{
		{
			Kind:    "http-server",
			Name:    "main",
			Driver:  "std",
			Options: rawJSON(httpServerOpt{Port: port1, Message: "v1"}),
		},
	}
	reconciler, err := reload.New(reg, initialSpecs)
	must(err)

	// Explicit first resolve to start the service.
	_, err = reconciler.Current().Resolve(context.Background(), resorch.ID{
		Kind: "http-server",
		Name: "main",
	})
	must(err)

	time.Sleep(time.Second)
	fmt.Println("first:", mustGET(port1))

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := reconciler.Reconcile(ctx, []resorch.NodeSpec{
		{
			Kind:    "http-server",
			Name:    "main",
			Driver:  "std",
			Options: rawJSON(httpServerOpt{Port: port2, Message: "v2"}),
		},
	})
	must(err)
	fmt.Printf("reconcile rebuilt=%d reused=%d\n", len(result.Rebuilt), len(result.Reused))

	time.Sleep(time.Second)
	_, oldErr := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port1))
	fmt.Println("old port closed:", oldErr != nil)
	fmt.Println("new:", mustGET(port2))

	must(reconciler.Current().Close(context.Background()))
}

func pickFreePort() int {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func mustGET(port int) string {
	resp, err := http.Get(fmt.Sprintf("http://127.0.0.1:%d", port))
	must(err)
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	must(err)
	return string(data)
}

func rawJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
