// Package memory provides an in-memory Storage implementation intended for use
// in tests. It records every SaveTransfer call so assertions can inspect what
// the watcher emitted without touching a real database.
package memory

import (
	"context"
	"sort"
	"strings"
	"sync"

	"github.com/leohhhn/tokentail/internal/storage"
)

// Store is a thread-safe in-memory Storage spy.
type Store struct {
	mu        sync.Mutex
	transfers []storage.Transfer
}

// New returns an empty, ready-to-use Store.
func New() *Store {
	return &Store{}
}

// SaveTransfer appends t to the in-memory list for later inspection by tests.
func (s *Store) SaveTransfer(_ context.Context, t storage.Transfer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transfers = append(s.transfers, t)
	return nil
}

// GetTransfers returns the stored transfers matching filter, newest first
// (highest block, then highest log index). Empty filter fields are ignored;
// address comparisons are case-insensitive.
func (s *Store) GetTransfers(_ context.Context, filter storage.TransferFilter) ([]storage.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	matched := make([]storage.Transfer, 0, len(s.transfers))
	for _, t := range s.transfers {
		if filter.Token != "" && !strings.EqualFold(t.Token, filter.Token) {
			continue
		}
		if filter.From != "" && !strings.EqualFold(t.From, filter.From) {
			continue
		}
		if filter.To != "" && !strings.EqualFold(t.To, filter.To) {
			continue
		}
		matched = append(matched, t)
	}

	// Newest first: order by block descending, breaking ties on log index.
	sort.Slice(matched, func(i, j int) bool {
		if matched[i].Block != matched[j].Block {
			return matched[i].Block > matched[j].Block
		}
		return matched[i].LogIndex > matched[j].LogIndex
	})

	// Apply offset, then limit, guarding against out-of-range values.
	if filter.Offset > 0 {
		if filter.Offset >= len(matched) {
			return []storage.Transfer{}, nil
		}
		matched = matched[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(matched) {
		matched = matched[:filter.Limit]
	}

	return matched, nil
}

// GetTransferByTxHash returns the matching transfer with the lowest log index,
// or (nil, nil) if none is found. Comparison is case-insensitive.
func (s *Store) GetTransferByTxHash(_ context.Context, txHash string) (*storage.Transfer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found *storage.Transfer
	for i := range s.transfers {
		t := s.transfers[i]
		if !strings.EqualFold(t.TxHash, txHash) {
			continue
		}
		if found == nil || t.LogIndex < found.LogIndex {
			copied := t
			found = &copied
		}
	}
	return found, nil
}

// Close is a no-op; the in-memory store requires no teardown.
func (s *Store) Close() error { return nil }

// Len returns the number of transfers recorded so far.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.transfers)
}

// Get returns the transfer at index i under the lock.
func (s *Store) Get(i int) storage.Transfer {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.transfers[i]
}

// Reset clears all recorded transfers.
func (s *Store) Reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.transfers = nil
}
