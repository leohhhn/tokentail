package watcher

import (
	"context"
	"fmt"
	"log"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

type Watcher struct {
	client *ethclient.Client
}

func New(ctx context.Context, rpcURL string) (*Watcher, error) {
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

	return &Watcher{client: client}, nil
}

func (w *Watcher) Close() {
	w.client.Close()
}

func (w *Watcher) Start(ctx context.Context) error {
	logs := make(chan types.Log)
	sub, err := w.client.SubscribeFilterLogs(ctx, ethereum.FilterQuery{}, logs)
	if err != nil {
		return fmt.Errorf("subscribe: %w", err)
	}
	defer sub.Unsubscribe()

	log.Println("subscribed to all logs")

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-sub.Err():
			return fmt.Errorf("subscription error: %w", err)
		case l := <-logs:
			printLog(l)
		}
	}
}

func printLog(l types.Log) {
	fmt.Printf("block=%-9d tx=%s  addr=%s  topics=%d  data=%dB\n",
		l.BlockNumber, l.TxHash.Hex(), l.Address.Hex(), len(l.Topics), len(l.Data))
	for i, topic := range l.Topics {
		fmt.Printf("  topic[%d] %s\n", i, topic.Hex())
	}
}
