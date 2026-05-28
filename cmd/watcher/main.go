package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/charmbracelet/huh"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
	"github.com/leohhhn/evm-watcher/internal/watcher"
)

func main() {
	// Try loading .env file
	_ = godotenv.Load()

	rpcURL := os.Getenv("ETH_RPC_URL")
	if rpcURL == "" {
		// Try prompting the user for the RPC URL if it's not set in .env
		var err error
		rpcURL, err = promptRPCURL()
		if err != nil {
			log.Fatalf("rpc url: %v", err)
		}
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	client, err := watcher.Dial(ctx, rpcURL)
	if err != nil {
		log.Fatalf("connect: %v", err)
	}

	cfg, err := promptConfig(ctx, client)
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	w, err := watcher.New(client, cfg)
	if err != nil {
		log.Fatalf("init watcher: %v", err)
	}
	defer w.Close()

	log.Println("watcher started — press Ctrl+C to stop")
	if err := w.Start(ctx); err != nil {
		log.Fatalf("watcher: %v", err)
	}
}

func promptRPCURL() (string, error) {
	var url string
	form := huh.NewForm(
		huh.NewGroup(
			huh.NewInput().
				Title("RPC URL").
				Description("A WebSocket URL is required for live log subscriptions.").
				Placeholder("wss://...").
				Validate(func(s string) error {
					if !strings.HasPrefix(s, "ws://") && !strings.HasPrefix(s, "wss://") {
						return fmt.Errorf("must be a WebSocket URL starting with ws:// or wss://")
					}
					return nil
				}).
				Value(&url),
		),
	)
	if err := form.Run(); err != nil {
		return "", err
	}
	return url, nil
}

func promptConfig(ctx context.Context, client *ethclient.Client) (watcher.Config, error) {
	// customIdx is the sentinel value bound to the "Custom address..." option in
	// the token select. -1 is intentionally out of range of watcher.Tokens indices
	// (which are 0-based), so it can never collide with a real token index.
	const customIdx = -1

	// Build select options: predefined tokens + custom.
	options := make([]huh.Option[int], len(watcher.Tokens)+1)
	for i, t := range watcher.Tokens {
		options[i] = huh.NewOption(t.Symbol, i)
	}
	options[len(watcher.Tokens)] = huh.NewOption("Custom address...", customIdx)

	var (
		tokenIdx     int
		customAddr   string
		minAmountStr string
		maxAmountStr string
		filterByAddr bool
		filterAddr   string
		outputFmt    watcher.OutputFormat
		outputPath   string
	)

	form := huh.NewForm(
		huh.NewGroup(
			huh.NewSelect[int]().
				Title("Which token do you want to watch?").
				Options(options...).
				Value(&tokenIdx),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Contract address").
				Placeholder("0x...").
				Validate(func(s string) error {
					if !common.IsHexAddress(s) {
						return fmt.Errorf("invalid Ethereum address")
					}
					return nil
				}).
				Value(&customAddr),
		).WithHideFunc(func() bool {
			return tokenIdx != customIdx
		}),
		huh.NewGroup(
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
			huh.NewInput().
				Title("Maximum transfer amount (0 = no limit)").
				Placeholder("0").
				Validate(func(s string) error {
					v, err := strconv.ParseFloat(s, 64)
					if err != nil || v < 0 {
						return fmt.Errorf("enter a positive number or 0")
					}
					return nil
				}).
				Value(&maxAmountStr),
		),
		huh.NewGroup(
			huh.NewConfirm().
				Title("Filter by a specific address?").
				Description("Only show transfers where this address is the sender or recipient.").
				Value(&filterByAddr),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Address to track").
				Placeholder("0x...").
				Validate(func(s string) error {
					if !common.IsHexAddress(s) {
						return fmt.Errorf("invalid Ethereum address")
					}
					return nil
				}).
				Value(&filterAddr),
		).WithHideFunc(func() bool {
			return !filterByAddr
		}),
		huh.NewGroup(
			huh.NewSelect[watcher.OutputFormat]().
				Title("Output format").
				Options(
					huh.NewOption("Print to terminal", watcher.FormatStdout),
					huh.NewOption("CSV file", watcher.FormatCSV),
					huh.NewOption("Markdown table", watcher.FormatMarkdown),
				).
				Value(&outputFmt),
		),
		huh.NewGroup(
			huh.NewInput().
				Title("Output file path").
				Placeholder("transfers.csv").
				Validate(func(s string) error {
					if strings.TrimSpace(s) == "" {
						return fmt.Errorf("file path cannot be empty")
					}
					return nil
				}).
				Value(&outputPath),
		).WithHideFunc(func() bool {
			return outputFmt == watcher.FormatStdout
		}),
	)

	if err := form.Run(); err != nil {
		return watcher.Config{}, err
	}

	var token watcher.Token
	if tokenIdx == customIdx {
		resolved, err := watcher.ResolveToken(ctx, client, common.HexToAddress(customAddr))
		if err != nil {
			return watcher.Config{}, fmt.Errorf("resolve token at %s: %w", customAddr, err)
		}
		token = resolved
		log.Printf("resolved: %s (%s)", token.Symbol, token.Address.Hex())
	} else {
		token = watcher.Tokens[tokenIdx]
	}

	minAmount, err := strconv.ParseFloat(minAmountStr, 64)
	if err != nil {
		return watcher.Config{}, fmt.Errorf("invalid min amount %q: %w", minAmountStr, err)
	}

	maxAmount, err := strconv.ParseFloat(maxAmountStr, 64)
	if err != nil {
		return watcher.Config{}, fmt.Errorf("invalid max amount %q: %w", maxAmountStr, err)
	}

	cfg := watcher.Config{
		Token:        token,
		MinAmount:    minAmount,
		MaxAmount:    maxAmount,
		OutputFormat: outputFmt,
		OutputPath:   outputPath,
	}
	if filterByAddr {
		cfg.FilterAddress = common.HexToAddress(filterAddr)
	}
	return cfg, nil
}
