// Package watcher subscribes to ERC-20 Transfer events and routes them to
// configurable output sinks (stdout, CSV, Markdown) and optional storage.
package watcher

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/leohhhn/tokentail/internal/storage"
)

var transferSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

// Token holds the on-chain identity and decimal precision of an ERC-20 token.
type Token struct {
	Symbol   string
	Address  common.Address
	Decimals *big.Float
}

// decimalsToFactor converts a token's decimal count into the divisor needed to
// convert raw uint256 amounts to human-readable values (e.g. 6 → 1_000_000).
// Uses integer exponentiation to avoid float64 precision loss for large decimals.
func decimalsToFactor(decimals uint8) *big.Float {
	exp := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Float).SetInt(exp)
}

var tokens = []Token{
	{Symbol: "USDC", Address: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"), Decimals: decimalsToFactor(6)},
	{Symbol: "USDT", Address: common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"), Decimals: decimalsToFactor(6)},
	{Symbol: "DAI", Address: common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F"), Decimals: decimalsToFactor(18)},
}

// AvailableTokens returns a copy of the built-in token list.
func AvailableTokens() []Token {
	out := make([]Token, len(tokens))
	copy(out, tokens)
	return out
}

// Config holds all runtime options that control what the Watcher observes and where it writes output.
type Config struct {
	Token         Token
	MinAmount     float64
	MaxAmount     float64
	FilterAddress common.Address // zero value means no filter
	OutputFormat  OutputFormat
	OutputPath    string         // only used when OutputFormat is not FormatStdout
	Store         storage.Storage // nil means no DB persistence
}

// Watcher subscribes to Transfer logs for a single ERC-20 token and writes matching events to its configured output.
type Watcher struct {
	client EthClient
	cfg    Config
	writer transferWriter
}

var chainNames = map[int64]string{
	1:        "Ethereum Mainnet",
	10:       "Optimism",
	56:       "BNB Smart Chain",
	137:      "Polygon",
	8453:     "Base",
	42161:    "Arbitrum One",
	43114:    "Avalanche C-Chain",
	11155111: "Sepolia",
}

// chainName returns a human-readable name for a chain ID, falling back to "chain <id>" for unknowns.
func chainName(id *big.Int) string {
	if id.IsInt64() {
		if name, ok := chainNames[id.Int64()]; ok {
			return name
		}
	}
	return fmt.Sprintf("chain %s", id)
}

// Dial connects to an Ethereum node and logs the chain name and ID.
func Dial(ctx context.Context, rpcURL string) (EthClient, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}
	chainID, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("get chain ID: %w", err)
	}
	log.Printf("connected to %s (chain ID %s)", chainName(chainID), chainID)
	return client, nil
}

// New creates a Watcher using the given client and config, opening the output writer.
func New(client EthClient, cfg Config) (*Watcher, error) {
	writer, err := newTransferWriter(cfg.OutputFormat, cfg.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("open output: %w", err)
	}
	return &Watcher{client: client, cfg: cfg, writer: writer}, nil
}

// Close flushes and closes the output writer, storage backend, and RPC client.
func (w *Watcher) Close() {
	if err := w.writer.close(); err != nil {
		log.Printf("closing output writer: %v", err)
	}
	if store := w.cfg.Store; store != nil {
		if err := store.Close(); err != nil {
			log.Printf("closing storage: %v", err)
		}
	}
	w.client.Close()
}

// Start subscribes to Transfer logs from the latest block and processes them until ctx is cancelled.
func (w *Watcher) Start(ctx context.Context) error {
	header, err := w.client.HeaderByNumber(ctx, nil)
	if err != nil {
		return fmt.Errorf("get latest block: %w", err)
	}

	query := ethereum.FilterQuery{
		FromBlock: header.Number,
		Addresses: []common.Address{w.cfg.Token.Address},
		Topics:    [][]common.Hash{{transferSig}},
	}

	logs := make(chan types.Log, 64)
	sub, err := w.client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	log.Printf("watching %s transfers (min %.2f %s, max %.2f %s) ",
		w.cfg.Token.Symbol,
		w.cfg.MinAmount,
		w.cfg.Token.Symbol,
		w.cfg.MaxAmount,
		w.cfg.Token.Symbol,
	)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-sub.Err():
			return fmt.Errorf("subscription error: %w", err)
		case l := <-logs:
			w.printLog(ctx, l)
		}
	}
}

// printLog applies all configured filters to a raw log and, if it passes, writes it to the output and storage.
func (w *Watcher) printLog(ctx context.Context, l types.Log) {
	if len(l.Topics) != 3 || l.Topics[0] != transferSig {
		return
	}

	from := common.HexToAddress(l.Topics[1].Hex())
	to := common.HexToAddress(l.Topics[2].Hex())

	if f := w.cfg.FilterAddress; f != (common.Address{}) && from != f && to != f {
		return
	}

	raw := new(big.Int).SetBytes(l.Data)
	amount, _ := new(big.Float).Quo(new(big.Float).SetInt(raw), w.cfg.Token.Decimals).Float64()

	if amount < w.cfg.MinAmount {
		return
	}
	if w.cfg.MaxAmount > 0 && amount > w.cfg.MaxAmount {
		return
	}

	var blockTime time.Time
	if header, err := w.client.HeaderByNumber(ctx, new(big.Int).SetUint64(l.BlockNumber)); err != nil {
		log.Printf("fetch block %d header: %v", l.BlockNumber, err)
	} else {
		blockTime = time.Unix(int64(header.Time), 0).UTC()
	}

	rec := transferRecord{
		Block:     l.BlockNumber,
		Timestamp: blockTime,
		TxHash:    l.TxHash.Hex(),
		From:      from.Hex(),
		To:        to.Hex(),
		Amount:    amount,
		Symbol:    w.cfg.Token.Symbol,
	}

	if err := w.writer.write(rec); err != nil {
		log.Printf("write error: %v", err)
	}

	if store := w.cfg.Store; store != nil {
		t := storage.Transfer{
			Block:     rec.Block,
			TxHash:    rec.TxHash,
			LogIndex:  uint(l.Index),
			Token:     rec.Symbol,
			From:      rec.From,
			To:        rec.To,
			Amount:    rec.Amount,
			Timestamp: rec.Timestamp,
			CreatedAt: time.Now(),
		}
		if err := store.SaveTransfer(ctx, t); err != nil {
			log.Printf("storage error: %v", err)
		}
	}
}
