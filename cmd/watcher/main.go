package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/joho/godotenv"
	"github.com/leohhhn/evm-watcher/internal/watcher"
)

func main() {
	_ = godotenv.Load()

	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		log.Fatal("ETH_RPC_URL is required (use a WebSocket URL: wss://...)")
	}

	cfg, err := promptConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	w, err := watcher.New(ctx, rpcURL, cfg)
	if err != nil {
		log.Fatalf("init: %v", err)
	}
	defer w.Close()

	log.Println("watcher started — press Ctrl+C to stop")
	if err := w.Start(ctx); err != nil {
		log.Fatalf("watcher: %v", err)
	}
}

func promptConfig() (watcher.Config, error) {
	options := make([]huh.Option[int], len(watcher.Tokens))
	for i, t := range watcher.Tokens {
		options[i] = huh.NewOption(t.Symbol, i)
	}

	var tokenIdx int
	var minAmountStr string

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Which token do you want to watch?").
				Options(options...).
				Value(&tokenIdx),
			huh.NewInput().
				Title("Minimum transfer amount").
				Placeholder("1000").
				Validate(func(s string) error {
					v, err := strconv.ParseFloat(s, 64)
					if err != nil || v < 0 {
						return fmt.Errorf("enter a positive number")
					}
					return nil
				}).
				Value(&minAmountStr),
		),
	)

	if err := form.Run(); err != nil {
		return watcher.Config{}, err
	}

	minAmount, _ := strconv.ParseFloat(minAmountStr, 64)
	return watcher.Config{
		Token:     watcher.Tokens[tokenIdx],
		MinAmount: minAmount,
	}, nil
}
