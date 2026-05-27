package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/leohhhn/evm-watcher/internal/watcher"
)

func main() {
	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL is required (use a WebSocket URL: wss://...)")
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	w, err := watcher.New(ctx, rpcURL)
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	defer w.Close()

	log.Println("watcher started — press Ctrl+C to stop")
	if err := w.Start(ctx); err != nil {
		log.Fatalf("watcher: %v", err)
	}
}
