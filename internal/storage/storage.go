// Package storage defines the persistence types and interface shared by all storage backends.
package storage

import (
	"context"
	"time"
)

// Transfer is the canonical record written to any storage backend.
type Transfer struct {
	Block     uint64
	TxHash    string
	LogIndex  uint
	Token     string
	From      string
	To        string
	Amount    float64
	Timestamp time.Time // time the block was mined (from block header)
	CreatedAt time.Time // time the record was inserted into storage
}

// Storage is the interface any persistence backend must satisfy.
// Implementations must be safe for concurrent use.
type Storage interface {
	SaveTransfer(ctx context.Context, t Transfer) error
	Close() error
}
