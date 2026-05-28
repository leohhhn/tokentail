# tokentail — Implementation Plan

## Current state

The watcher connects to an EVM node, subscribes to ERC-20 Transfer events, applies filters, and writes output to stdout, CSV, or Markdown.

- `Storage` interface and `Transfer` struct defined in `internal/storage/storage.go`
- `EthClient` interface extracted in `internal/watcher/client.go`
- In-memory `Storage` implementation in `internal/storage/memory/`
- `Watcher.Config` accepts an optional `Store storage.Storage`; each matched transfer is written to it if non-nil
- 13 unit tests covering filter logic (`TestPrintLog_*`, `TestDecimalsToFactor`) and output writers (`TestCSVWriter_*`, `TestMarkdownWriter_*`)

No PostgreSQL implementation, integration tests, or `DATABASE_URL` wiring yet.

---

## Phase 1 — Storage abstraction

Before writing a PostgreSQL implementation, define a `Storage` interface so the watcher is decoupled from any specific backend. This makes PostgreSQL swappable for SQLite, an in-memory store (for tests), or any future target.

### Interface

```go
// internal/storage/storage.go

type Transfer struct {
    Block     uint64
    TxHash    string
    From      string
    To        string
    Amount    float64
    Token     string
    LogIndex  uint
    CreatedAt time.Time
}

type Storage interface {
    SaveTransfer(ctx context.Context, t Transfer) error
    Close() error
}
```

Keep `Transfer` here (not in the watcher package) — it is the storage layer's concern, not the watcher's. The watcher will depend on the interface, not on any implementation.

### Wiring

The `Watcher` gains an optional `Storage` field. If non-nil, each matched transfer is written to it in addition to (or instead of) the file/stdout writer. The two output paths are independent.

```
watcher.Config {
    ...
    Store storage.Storage  // nil = no DB persistence
}
```

---

## Phase 2 — PostgreSQL implementation

### Package layout

```
internal/
  storage/
    storage.go        # Storage interface + Transfer struct
    postgres/
      postgres.go     # pgx-based implementation
      migrations/
        001_create_transfers.sql
```

### Schema

```sql
CREATE TABLE transfers (
    id         BIGSERIAL PRIMARY KEY,
    block      BIGINT        NOT NULL,
    tx_hash    CHAR(66)      NOT NULL,
    log_index  INT           NOT NULL,
    token      VARCHAR(20)   NOT NULL,
    from_addr  CHAR(42)      NOT NULL,
    to_addr    CHAR(42)      NOT NULL,
    amount     NUMERIC(36,6) NOT NULL,
    created_at TIMESTAMPTZ   NOT NULL DEFAULT NOW(),

    UNIQUE (tx_hash, log_index)   -- deduplication guard
);

CREATE INDEX idx_transfers_from  ON transfers (from_addr);
CREATE INDEX idx_transfers_to    ON transfers (to_addr);
CREATE INDEX idx_transfers_block ON transfers (block);
```

`NUMERIC(36,6)` avoids floating-point rounding in the DB. The unique constraint on `(tx_hash, log_index)` prevents duplicate inserts if the watcher restarts mid-block.

### Driver

Use [`pgx/v5`](https://github.com/jackc/pgx) directly (`pgxpool` for connection pooling). Avoid `database/sql` — pgx's native interface is strictly better for PostgreSQL.

### Migrations

Use [`golang-migrate`](https://github.com/golang-migrate/migrate) with SQL files embedded via `//go:embed`. Run migrations automatically on startup before the watcher subscribes.

### Configuration

```bash
# .env
DATABASE_URL=postgres://user:pass@localhost:5432/evm_watcher?sslmode=disable
```

If `DATABASE_URL` is not set, the watcher starts without DB persistence (no error).

---

## Phase 3 — Testing

### Layers

| Layer | What is mocked | What is real | Tool |
|-------|---------------|--------------|------|
| Unit | ethclient, Storage | watcher filter logic, output formatting | `testify/mock` or hand-written interface stubs |
| Integration | nothing | pgx + real PostgreSQL (via Docker) | `testcontainers-go` or a local `docker-compose` test DB |

### Unit tests

Focus on the logic that can be tested without network or DB:

- `printLog` / filter logic: construct `types.Log` values directly and assert the mock storage receives or does not receive a `SaveTransfer` call
- `decimalsToFactor`: table-driven tests for edge cases (0, 6, 18, 255 decimals)
- `ResolveToken`: mock `ethclient` behind an interface so no RPC is needed
- `chainName`: table-driven, trivial

The key enabler is extracting the `ethclient` dependency behind an interface:

```go
// internal/watcher/client.go
type EthClient interface {
    ChainID(ctx context.Context) (*big.Int, error)
    HeaderByNumber(ctx context.Context, number *big.Int) (*types.Header, error)
    SubscribeFilterLogs(ctx context.Context, q ethereum.FilterQuery, ch chan<- types.Log) (ethereum.Subscription, error)
    CallContract(ctx context.Context, msg ethereum.CallMsg, blockNumber *big.Int) ([]byte, error)
    Close()
}
```

`*ethclient.Client` satisfies this interface already — no changes to production code, only the type of the field in `Watcher` changes from concrete to interface.

### Integration tests

- Spin up a PostgreSQL container using `testcontainers-go`
- Run migrations against it
- Insert known transfers via `SaveTransfer`
- Assert correct rows, deduplication behaviour, index usage via `EXPLAIN`
- Tear down the container after the test suite

### File structure (after all phases)

```
tokentail/
├── cmd/watcher/
│   └── main.go
├── internal/
│   ├── watcher/
│   │   ├── watcher.go
│   │   ├── client.go       # EthClient interface
│   │   ├── resolve.go
│   │   └── output.go
│   └── storage/
│       ├── storage.go      # Storage interface + Transfer
│       ├── memory/
│       │   └── memory.go   # In-memory impl (used in unit tests)
│       └── postgres/
│           ├── postgres.go
│           └── migrations/
│               └── 001_create_transfers.sql
├── tests/
│   └── integration/
│       └── postgres_test.go
├── docker-compose.yml      # local dev DB
├── .env.example
├── go.mod
└── README.md
```

---

## Order of work

1. ~~Define `Storage` interface and `Transfer` struct — no DB yet~~ ✓
2. ~~Extract `EthClient` interface in the watcher package~~ ✓
3. ~~Write unit tests for filter logic using a mock `Storage` and mock `EthClient`~~ ✓
4. ~~Write in-memory `Storage` implementation (used by unit tests as a spy)~~ ✓
5. Add PostgreSQL implementation with migrations
6. Write integration tests with `testcontainers-go`
7. Wire `DATABASE_URL` into `main.go` and the huh form (optional DB toggle)
