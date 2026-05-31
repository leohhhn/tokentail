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

## Phase 2 — REST API & server mode

### Overview

Add a `--server` flag that launches tokentail in server mode: reads config from environment variables, starts the watcher in the background, and exposes an HTTP API for querying collected transfers. The default (no flag) retains the existing interactive CLI behaviour.

### Run modes

| Mode | Trigger | Behaviour |
|------|---------|-----------|
| CLI (default) | `go run ./cmd/watcher` | Interactive huh form, output to stdout/CSV/Markdown |
| Server | `go run ./cmd/watcher --server` | Reads config from env, runs watcher + HTTP server |

### HTTP endpoints

| Method | Path | Description |
|--------|------|-------------|
| `GET` | `/transfers` | List collected transfers; supports `?token=`, `?from=`, `?to=`, `?limit=`, `?offset=` |
| `GET` | `/transfers/{txHash}` | Fetch a single transfer by transaction hash |
| `GET` | `/status` | Watcher status: connected chain, token, filter config, transfer count |

### Storage interface additions

Server mode requires read access to stored transfers. Extend the `Storage` interface:

```go
type Storage interface {
    SaveTransfer(ctx context.Context, t Transfer) error
    GetTransfers(ctx context.Context, filter TransferFilter) ([]Transfer, error)
    GetTransferByTxHash(ctx context.Context, txHash string) (*Transfer, error)
    Close() error
}

type TransferFilter struct {
    Token  string
    From   string
    To     string
    Limit  int
    Offset int
}
```

When no `DATABASE_URL` is set, falls back to the in-memory store so the API serves whatever was accumulated since the watcher started.

### Configuration (server mode)

All config is read from the environment — no interactive form:

```bash
# .env
ETH_RPC_URL=wss://...
TOKEN_ADDRESS=0xA0b86991c6218b36c1d19D4a2e9Eb0cE3606eB48
MIN_AMOUNT=0
MAX_AMOUNT=0
SERVER_PORT=8080        # default: 8080
```

### Package layout (after this phase)

```
tokentail/
├── cmd/watcher/
│   └── main.go              # --server flag; env-based config path
├── internal/
│   ├── api/
│   │   ├── server.go        # HTTP server setup, graceful shutdown
│   │   └── handlers.go      # Handler functions, JSON encoding
│   ├── watcher/
│   │   └── ...
│   └── storage/
│       └── ...
```

Uses standard `net/http` — no external router dependency.

---

## Phase 3 — PostgreSQL implementation

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
    ts         TIMESTAMPTZ   NOT NULL,
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

## Phase 4 — Testing

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
│   ├── api/
│   │   ├── server.go
│   │   └── handlers.go
│   ├── watcher/
│   │   ├── watcher.go
│   │   ├── client.go       # EthClient interface
│   │   ├── resolve.go
│   │   ├── output.go
│   │   ├── watcher_test.go
│   │   └── output_test.go
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

---

## Phase 5 — Docker

### Goal

A single `docker compose up` should start the watcher in server mode + a PostgreSQL database, with no local Go toolchain required.

### Files

```
tokentail/
├── Dockerfile               # multi-stage: build → minimal runtime image
├── docker-compose.yml       # watcher + postgres services
└── .env.example             # documents all required env vars
```

### Dockerfile

Multi-stage build to keep the runtime image small:

```dockerfile
# Stage 1 — build
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o tokentail ./cmd/watcher

# Stage 2 — runtime
FROM alpine:3.21
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/tokentail .
ENTRYPOINT ["./tokentail", "--server"]
```

`ca-certificates` is needed for TLS connections to WSS RPC endpoints.

### docker-compose.yml

```yaml
services:
  db:
    image: postgres:17-alpine
    environment:
      POSTGRES_USER: tokentail
      POSTGRES_PASSWORD: tokentail
      POSTGRES_DB: tokentail
    volumes:
      - pgdata:/var/lib/postgresql/data
    healthcheck:
      test: ["CMD-SHELL", "pg_isready -U tokentail"]
      interval: 5s
      timeout: 5s
      retries: 5

  watcher:
    build: .
    depends_on:
      db:
        condition: service_healthy
    env_file: .env
    environment:
      DATABASE_URL: postgres://tokentail:tokentail@db:5432/tokentail?sslmode=disable
    ports:
      - "8080:8080"

volumes:
  pgdata:
```

The `healthcheck` + `depends_on: condition: service_healthy` ensures the watcher waits for Postgres to be ready before starting, so migrations don't race the DB.

---

## Phase 6 — Observability

Production services need to expose their internal state. Three pillars:

### Structured logging

Replace `fmt.Printf` / `log.Printf` with [`zerolog`](https://github.com/rs/zerolog). Every log line is a JSON object with `level`, `time`, `component`, and contextual fields (block number, tx hash, token address). Makes logs parseable by Datadog, Loki, CloudWatch, etc.

```go
log.Info().
    Uint64("block", transfer.Block).
    Str("tx", transfer.TxHash).
    Float64("amount", transfer.Amount).
    Msg("transfer matched")
```

### Prometheus metrics

Expose `GET /metrics` (standard Prometheus scrape endpoint). Key metrics:

| Metric | Type | Description |
|--------|------|-------------|
| `tokentail_transfers_total` | Counter | Transfers matched since startup |
| `tokentail_blocks_processed_total` | Counter | Blocks seen |
| `tokentail_rpc_reconnects_total` | Counter | Websocket reconnections |
| `tokentail_api_requests_total` | Counter | HTTP requests by method + path + status |
| `tokentail_api_request_duration_seconds` | Histogram | API latency |

Add a `prometheus` service to `docker-compose.yml` + a Grafana service with a pre-built dashboard JSON — so `docker compose up` gives you a live dashboard out of the box.

### Health & readiness endpoints

Required for load balancers and Kubernetes probes:

| Endpoint | Returns | Healthy when |
|----------|---------|--------------|
| `GET /healthz` | `200 OK` | Process is alive |
| `GET /readyz` | `200 OK` | DB reachable + watcher subscribed |

`/readyz` returns `503` during startup or after RPC disconnect so a load balancer can remove the instance from rotation.

---

## Phase 7 — Resilience & correctness

These close the gap between "it runs" and "it runs reliably."

### Graceful shutdown

On `SIGTERM`/`SIGINT`: stop accepting new API requests, drain in-flight handlers (with a deadline), close the watcher subscription, flush any pending DB writes, then exit 0. Required for zero-downtime deploys and clean container stops.

### RPC reconnection

WebSocket connections to EVM nodes drop. The watcher should reconnect with exponential backoff (cap ~30s), re-subscribe to the filter, and continue from the last processed block. Log each reconnect attempt and increment `tokentail_rpc_reconnects_total`.

### Block reorg handling

Reorgs can invalidate transfers already written to the DB. When the watcher receives a log with `Removed: true` (go-ethereum sets this on reorgs), delete the corresponding row from `transfers` using `(tx_hash, log_index)`. Add a `removed` migration if needed.

### Multi-token support

Currently one token per watcher instance. Extend `Config` to accept a slice of token addresses; the filter query passes all addresses to `FilterQuery.Addresses`. The `transfers` table already stores `token` per row so no schema change is needed.

---

## Phase 8 — Features

### Historical backfill

Add a `--backfill-from <block>` flag (or `BACKFILL_FROM` env var in server mode). On startup, before subscribing to new logs, scan past blocks using `FilterLogs` and insert any matching transfers. Uses the same `SaveTransfer` path so deduplication is free via the `(tx_hash, log_index)` unique constraint.

### WebSocket streaming endpoint

`GET /ws/transfers` — upgrades to WebSocket and pushes each new matched transfer as a JSON object in real time. The watcher broadcasts to an internal fan-out bus; the WS handler subscribes to it. Useful for dashboards that don't want to poll.

### Webhook notifications

`POST /webhooks` — register a URL + optional filter (min amount, specific address). When a transfer matches, POST a JSON payload to the registered URL with retry logic (3 attempts, exponential backoff). Webhooks are stored in the DB so they survive restarts.

### API key authentication

Add an `api_keys` table. Requests to non-public endpoints require `Authorization: Bearer <key>`. Key creation is handled via a CLI command (`tokentail keys create`) or a seeded key from an env var for simple deployments.

---

## Phase 9 — CI/CD

### GitHub Actions

```
.github/workflows/
├── ci.yml       # on push/PR: go vet, golangci-lint, go test ./...
└── release.yml  # on tag: build multi-arch image, push to GHCR
```

**`ci.yml` steps:**
1. `golangci-lint` (includes `errcheck`, `staticcheck`, `gosec`)
2. `go test ./... -race -count=1` — race detector on every run
3. Integration tests via `testcontainers-go` (spins up Postgres in CI)

**`release.yml` steps:**
1. `docker buildx build --platform linux/amd64,linux/arm64`
2. Push to `ghcr.io/<owner>/tokentail:<tag>` and `:latest`
3. Create GitHub Release with changelog from `git log`

Multi-arch (`amd64` + `arm64`) means the image runs natively on Apple Silicon and AWS Graviton without emulation.

### Versioning

Embed build metadata at compile time via `-ldflags`:

```go
// cmd/watcher/main.go
var (
    version = "dev"
    commit  = "none"
    date    = "unknown"
)
```

Exposed on `GET /status` so you can always tell which build is running.

---

## Order of work

1. ~~Define `Storage` interface and `Transfer` struct — no DB yet~~ ✓
2. ~~Extract `EthClient` interface in the watcher package~~ ✓
3. ~~Write unit tests for filter logic using a mock `Storage` and mock `EthClient`~~ ✓
4. ~~Write in-memory `Storage` implementation (used by unit tests as a spy)~~ ✓
5. ~~Extend `Storage` interface with read methods (`GetTransfers`, `GetTransferByTxHash`)~~ ✓
6. Add `internal/api` package with HTTP server and handlers
7. Wire `--server` flag into `main.go`; env-based config path
8. Add PostgreSQL implementation with migrations
9. Write integration tests with `testcontainers-go`
10. Wire `DATABASE_URL` into `main.go`
11. Write `Dockerfile` (multi-stage build) and `docker-compose.yml`
12. Write `.env.example` documenting all required vars
13. Add structured logging with `zerolog`
14. Add Prometheus metrics + `/metrics` endpoint
15. Add `/healthz` and `/readyz` endpoints
16. Add Grafana + Prometheus to `docker-compose.yml`
17. Implement graceful shutdown (SIGTERM drain)
18. Implement RPC reconnection with exponential backoff
19. Handle block reorg (`Removed: true` log events)
20. Multi-token support (`Config.Tokens []string`)
21. Historical backfill (`--backfill-from <block>`)
22. WebSocket streaming endpoint (`/ws/transfers`)
23. Webhook notifications with retry
24. API key authentication
25. GitHub Actions CI (lint + test + integration)
26. GitHub Actions release (multi-arch image → GHCR)
27. Embed version/commit/date via `-ldflags`
