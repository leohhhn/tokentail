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

// add chain filter
// add option to write out to file instead of stdout

var transferSig = crypto.Keccak256Hash([]byte("Transfer(address,address,uint256)"))

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

// Tokens is the set of popular tokens available as quick-select options.
var Tokens = []Token{
	{Symbol: "USDC", Address: common.HexToAddress("0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48"), Decimals: decimalsToFactor(6)},
	{Symbol: "USDT", Address: common.HexToAddress("0xdAC17F958D2ee523a2206206994597C13D831ec7"), Decimals: decimalsToFactor(6)},
	{Symbol: "DAI", Address: common.HexToAddress("0x6B175474E89094C44Da98b954EedeAC495271d0F"), Decimals: decimalsToFactor(18)},
}

type Config struct {
	Token         Token
	MinAmount     float64
	MaxAmount     float64
	FilterAddress common.Address // zero value means no filter
	OutputFormat  OutputFormat
	OutputPath    string // only used when OutputFormat is not FormatStdout
}

type Watcher struct {
	client *ethclient.Client
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

func chainName(id *big.Int) string {
	if name, ok := chainNames[id.Int64()]; ok {
		return name
	}
	return fmt.Sprintf("chain %s", id)
}

// Dial connects to an Ethereum node and logs the chain name and ID.
func Dial(ctx context.Context, rpcURL string) (*ethclient.Client, error) {
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

func New(client *ethclient.Client, cfg Config) (*Watcher, error) {
	writer, err := newTransferWriter(cfg.OutputFormat, cfg.OutputPath)
	if err != nil {
		return nil, fmt.Errorf("open output: %w", err)
	}
	return &Watcher{client: client, cfg: cfg, writer: writer}, nil
}

func (w *Watcher) Close() {
	w.writer.close()
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
			w.printLog(l)
		}
	}
}

func (w *Watcher) printLog(l types.Log) {
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

	if err := w.writer.write(transferRecord{
		Block:  l.BlockNumber,
		TxHash: l.TxHash.Hex(),
		From:   from.Hex(),
		To:     to.Hex(),
		Amount: amount,
		Symbol: w.cfg.Token.Symbol,
	}); err != nil {
		log.Printf("write error: %v", err)
	}
}
