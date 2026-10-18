package main

import (
	"context"
	"flag"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"shiying/internal/app"
)

func main() {
	if err := run(); err != nil {
		slog.Error("服务启动失败", "error", err)
		os.Exit(1)
	}
}
func run() error {
	defaultAddr := os.Getenv("SHIYING_ADDR")
	if defaultAddr == "" {
		defaultAddr = "127.0.0.1:8080"
	}
	defaultData := os.Getenv("SHIYING_DATA_DIR")
	if defaultData == "" {
		defaultData = "./data"
	}
	addr := flag.String("addr", defaultAddr, "HTTP 监听地址")
	data := flag.String("data", defaultData, "SQLite 和视频的存储目录")
	flag.Parse()
	token := os.Getenv("SHIYING_ACCESS_TOKEN")
	if err := app.ValidateListen(*addr, token); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	defer listener.Close()
	s, err := app.NewServer(*data, token)
	if err != nil {
		return err
	}
	defer s.Close()
	server := &http.Server{Addr: *addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errors := make(chan error, 1)
	go func() { slog.Info("拾影已启动", "url", "http://"+*addr); errors <- server.Serve(listener) }()
	select {
	case err := <-errors:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return server.Shutdown(shutdown)
}
