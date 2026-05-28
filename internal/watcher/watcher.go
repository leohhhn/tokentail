package watcher

import (
	"context"
	"fmt"
	"log"
	"math/big"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var transferSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

type Token struct {
	Symbol   string
	Address  common.Address
	Decimals *big.Float
}

// Tokens is the set of supported stablecoins.
var Tokens = []Token{
	{
		Symbol:   "USDC",
		Address:  common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"),
		Decimals: big.NewFloat(1e6),
	},
	{
		Symbol:   "USDT",
		Address:  common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"),
		Decimals: big.NewFloat(1e6),
	},
	{
		Symbol:   "DAI",
		Address:  common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F"),
		Decimals: big.NewFloat(1e18),
	},
}

type Config struct {
	Token     Token
	MinAmount float64
}

type Watcher struct {
	client *ethclient.Client
	cfg    Config
}

func New(ctx context.Context, rpcURL string, cfg Config) (*Watcher, error) {
	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("dial: %w", err)
	}

	chainID, err := client.ChainID(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("get chain ID: %w", err)
	}
	log.Printf("connected to chain ID %s", chainID)

	return &Watcher{client: client, cfg: cfg}, nil
}

func (w *Watcher) Close() {
	w.client.Close()
}

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
	logs := make(chan types.Log)
	sub, err := w.client.SubscribeFilterLogs(ctx, query, logs)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	log.Printf("watching %s transfers (min %.2f %s)", w.cfg.Token.Symbol, w.cfg.MinAmount, w.cfg.Token.Symbol)

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-sub.Err():
			return fmt.Errorf("subscription error: %w", err)
		case l := <-logs:
			w.printLog(l)
		}
	}
}

func (w *Watcher) printLog(l types.Log) {
	if len(l.Topics) < 3 {
		return
	}

	raw := new(big.Int).SetBytes(l.Data)
	amount, _ := new(big.Float).Quo(new(big.Float).SetInt(raw), w.cfg.Token.Decimals).Float64()

	if amount < w.cfg.MinAmount {
		return
	}

	from := common.HexToAddress(l.Topics[1].Hex())
	to := common.HexToAddress(l.Topics[2].Hex())

	fmt.Printf("block=%-9d  tx=%s\n  from=%s\n  to  =%s\n  amount=%.2f %s\n",
		l.BlockNumber, l.TxHash.Hex(), from.Hex(), to.Hex(), amount, w.cfg.Token.Symbol)
}
