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

// TransferFilter narrows a GetTransfers query. Zero-valued fields are ignored,
// so an empty filter matches every transfer. Address fields are matched
// case-insensitively against the stored values.
type TransferFilter struct {
	Token  string // contract address; "" matches any token
	From   string // sender address; "" matches any sender
	To     string // recipient address; "" matches any recipient
	Limit  int    // max rows to return; <= 0 means no limit
	Offset int    // rows to skip from the start; <= 0 means none
}

// Storage is the interface any persistence backend must satisfy.
// Implementations must be safe for concurrent use.
type Storage interface {
	// SaveTransfer persists a single transfer. Implementations should treat
	// (TxHash, LogIndex) as a unique key and ignore duplicates.
	SaveTransfer(ctx context.Context, t Transfer) error

	// GetTransfers returns the transfers matching filter, newest first.
	GetTransfers(ctx context.Context, filter TransferFilter) ([]Transfer, error)

	// GetTransferByTxHash returns the transfer with the given transaction hash,
	// or (nil, nil) if no such transfer exists. When a transaction emits
	// multiple matching transfers, the lowest LogIndex is returned.
	GetTransferByTxHash(ctx context.Context, txHash string) (*Transfer, error)

	// Close releases any resources held by the backend.
	Close() error
}
