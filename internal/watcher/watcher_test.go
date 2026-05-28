package watcher

import (
	"context"
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/leohhhn/tokentail/internal/storage"
	"github.com/leohhhn/tokentail/internal/storage/memory"
)

// mockBlockTime is the fixed Unix timestamp returned by mockClient for all blocks.
const mockBlockTime = 1_700_000_000

// mockClient is a minimal EthClient that returns a fixed block header timestamp.
type mockClient struct{}

func (m *mockClient) ChainID(_ context.Context) (*big.Int, error) { return big.NewInt(1), nil }
func (m *mockClient) HeaderByNumber(_ context.Context, _ *big.Int) (*types.Header, error) {
	return &types.Header{Time: mockBlockTime}, nil
}
func (m *mockClient) SubscribeFilterLogs(_ context.Context, _ ethereum.FilterQuery, _ chan<- types.Log) (ethereum.Subscription, error) {
	return nil, nil
}
func (m *mockClient) CallContract(_ context.Context, _ ethereum.CallMsg, _ *big.Int) ([]byte, error) {
	return nil, nil
}
func (m *mockClient) Close() {}

// buildLog constructs a synthetic ERC-20 Transfer log for use in tests.
// from and to are packed as left-padded 32-byte topics; amount is ABI-encoded
// as a 32-byte big-endian uint256 in Data.
func buildLog(block uint64, from, to common.Address, amount *big.Int) types.Log {
	pad := func(addr common.Address) common.Hash {
		var h common.Hash
		copy(h[12:], addr[:])
		return h
	}

	data := make([]byte, 32)
	amount.FillBytes(data)

	return types.Log{
		BlockNumber: block,
		TxHash:      common.HexToHash("0xdeadbeef"),
		Index:       0,
		Topics:      []common.Hash{transferSig, pad(from), pad(to)},
		Data:        data,
	}
}

func newTestWatcher(cfg Config, store *memory.Store) *Watcher {
	cfg.Store = store
	return &Watcher{
		client:      &mockClient{},
		cfg:         cfg,
		writer:      &stdoutWriter{},
		headerCache: make(map[uint64]*types.Header),
	}
}

// --- decimalsToFactor ---

func TestDecimalsToFactor(t *testing.T) {
	cases := []struct {
		decimals uint8
		want     float64
	}{
		{0, 1},
		{6, 1e6},
		{18, 1e18},
	}
	for _, tc := range cases {
		got, _ := decimalsToFactor(tc.decimals).Float64()
		if got != tc.want {
			t.Errorf("decimalsToFactor(%d) = %g, want %g", tc.decimals, got, tc.want)
		}
	}
}

// --- filter: amount ---

func TestPrintLog_BelowMinAmount(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{
		Token:     tokens[0], // USDC, 6 decimals
		MinAmount: 1000,
	}, store)

	// 500 USDC = 500_000_000 raw
	raw := new(big.Int).Mul(big.NewInt(500), big.NewInt(1e6))
	w.printLog(context.Background(), buildLog(1, common.Address{1}, common.Address{2}, raw))

	if store.Len() != 0 {
		t.Errorf("expected no transfers stored, got %d", store.Len())
	}
}

func TestPrintLog_AboveMinAmount(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{
		Token:     tokens[0],
		MinAmount: 1000,
	}, store)

	// 2000 USDC
	raw := new(big.Int).Mul(big.NewInt(2000), big.NewInt(1e6))
	w.printLog(context.Background(), buildLog(1, common.Address{1}, common.Address{2}, raw))

	if store.Len() != 1 {
		t.Errorf("expected 1 transfer stored, got %d", store.Len())
	}
}

func TestPrintLog_AboveMaxAmount(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{
		Token:     tokens[0],
		MinAmount: 0,
		MaxAmount: 500,
	}, store)

	// 1000 USDC — above max
	raw := new(big.Int).Mul(big.NewInt(1000), big.NewInt(1e6))
	w.printLog(context.Background(), buildLog(1, common.Address{1}, common.Address{2}, raw))

	if store.Len() != 0 {
		t.Errorf("expected no transfers stored, got %d", store.Len())
	}
}

func TestPrintLog_MaxAmountZeroMeansNoLimit(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{
		Token:     tokens[0],
		MinAmount: 0,
		MaxAmount: 0, // no limit
	}, store)

	raw := new(big.Int).Mul(big.NewInt(999_999_999), big.NewInt(1e6))
	w.printLog(context.Background(), buildLog(1, common.Address{1}, common.Address{2}, raw))

	if store.Len() != 1 {
		t.Errorf("expected 1 transfer stored, got %d", store.Len())
	}
}

// --- filter: address ---

func TestPrintLog_AddressFilterMatch(t *testing.T) {
	store := memory.New()
	target := common.HexToAddress("0x1111111111111111111111111111111111111111")
	other := common.HexToAddress("0x2222222222222222222222222222222222222222")

	w := newTestWatcher(Config{
		Token:         tokens[0],
		FilterAddress: target,
	}, store)

	raw := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e6))

	// target is sender — should pass
	w.printLog(context.Background(), buildLog(1, target, other, raw))
	// target is recipient — should pass
	w.printLog(context.Background(), buildLog(2, other, target, raw))
	// target not involved — should be filtered
	w.printLog(context.Background(), buildLog(3, other, common.Address{9}, raw))

	if store.Len() != 2 {
		t.Errorf("expected 2 transfers stored, got %d", store.Len())
	}
}

// --- malformed log guard ---

func TestPrintLog_WrongTopicCount(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{Token: tokens[0]}, store)

	l := types.Log{
		BlockNumber: 1,
		Topics:      []common.Hash{transferSig}, // missing from/to
		Data:        make([]byte, 32),
	}
	w.printLog(context.Background(), l) // must not panic

	if store.Len() != 0 {
		t.Errorf("expected no transfers stored for malformed log, got %d", store.Len())
	}
}

func TestPrintLog_WrongTopic0(t *testing.T) {
	store := memory.New()
	w := newTestWatcher(Config{Token: tokens[0]}, store)

	raw := new(big.Int).Mul(big.NewInt(100), big.NewInt(1e6))
	l := buildLog(1, common.Address{1}, common.Address{2}, raw)
	l.Topics[0] = common.HexToHash("0xdeadbeef") // not a Transfer sig

	w.printLog(context.Background(), l)

	if store.Len() != 0 {
		t.Errorf("expected no transfers stored for wrong topic, got %d", store.Len())
	}
}

// --- storage fields ---

func TestPrintLog_StorageFields(t *testing.T) {
	store := memory.New()
	from := common.HexToAddress("0xAAAA000000000000000000000000000000000001")
	to := common.HexToAddress("0xBBBB000000000000000000000000000000000002")

	w := newTestWatcher(Config{Token: tokens[0]}, store)

	raw := new(big.Int).Mul(big.NewInt(500), big.NewInt(1e6))
	w.printLog(context.Background(), buildLog(42, from, to, raw))

	if store.Len() != 1 {
		t.Fatalf("expected 1 transfer, got %d", store.Len())
	}
	got := store.Get(0)
	if got.Block != 42 {
		t.Errorf("Block: got %d, want 42", got.Block)
	}
	if got.From != from.Hex() {
		t.Errorf("From: got %s, want %s", got.From, from.Hex())
	}
	if got.To != to.Hex() {
		t.Errorf("To: got %s, want %s", got.To, to.Hex())
	}
	if got.Amount != 500 {
		t.Errorf("Amount: got %f, want 500", got.Amount)
	}
	if got.Token != "USDC" {
		t.Errorf("Token: got %s, want USDC", got.Token)
	}
	wantTime := time.Unix(mockBlockTime, 0).UTC()
	if !got.Timestamp.Equal(wantTime) {
		t.Errorf("Timestamp: got %v, want %v", got.Timestamp, wantTime)
	}
}

// Compile-time check: *memory.Store satisfies storage.Storage.
var _ storage.Storage = (*memory.Store)(nil)

func BenchmarkPrintLog(b *testing.B) {
      store := memory.New()
      w := newTestWatcher(Config{
          Token:     tokens[0], // USDC
          MinAmount: 0,
      }, store)

      from := common.Address{1}
      to := common.Address{2}
      raw := new(big.Int).Mul(big.NewInt(5000), big.NewInt(1e6)) // 5000 USDC

      // Pre-build logs across N distinct blocks to exercise both cache hit and miss paths
      logs := make([]types.Log, b.N)
      for i := range logs {
          logs[i] = buildLog(uint64(i%100), from, to, raw) // 100 unique blocks → cache saturates quickly
      }

      b.ResetTimer()
      for i := range logs {
          w.printLog(context.Background(), logs[i])
      }
  }
