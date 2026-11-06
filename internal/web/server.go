package web

import (
	"context"
	"embed"
	"io/fs"
	"net/http"
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	pb "github.com/viviwu/tradeforge/gen/tradeforge/v1"
)

//go:embed static/*
var staticFS embed.FS

func NewHandler(ctx context.Context, grpcAddr string, metrics http.Handler) (http.Handler, error) {
	mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(headerMatcher))
	opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}
	if err := pb.RegisterAuthServiceHandlerFromEndpoint(ctx, mux, grpcAddr, opts); err != nil {
		return nil, err
	}
	if err := pb.RegisterTradeServiceHandlerFromEndpoint(ctx, mux, grpcAddr, opts); err != nil {
		return nil, err
	}

	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		return nil, err
	}
	root := http.NewServeMux()
	root.Handle("/v1/", mux)
	root.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	if metrics != nil {
		root.Handle("/metrics", metrics)
	}
	root.Handle("/", http.FileServer(http.FS(sub)))
	return root, nil
}

func headerMatcher(key string) (string, bool) {
	if strings.EqualFold(key, "authorization") {
		return "authorization", true
	}
	return runtime.DefaultHeaderMatcher(key)
}
