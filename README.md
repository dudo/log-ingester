# Log ingester

A Go service that accepts timestamped text logs, inserts them into ClickHouse, and queries them by level, service, or time range.

Each line contains an RFC 3339 timestamp, a level, a service name, and the remaining message. Lines support up to microsecond timestamp precision. The parser accepts a plain level such as `ERROR` and normalizes a pasted `**ERROR**`. UUIDs and latency annotations in messages are preserved as text. Requests can contain up to 256 KiB, with an 8 KiB limit per line. A malformed line rejects the whole request before insertion.

```text
2026-03-04T09:13:01.245234Z ERROR dispatch Dispatch lease expired before pickup
```

## Run locally

Use Go 1.27 and Docker Compose. Start ClickHouse and the Go service.

```sh
docker compose up -d --wait
go run ./cmd/log-ingester
```

In another terminal, ingest the synthetic fixture and query it.

```sh
curl -si --data-binary @testdata/demo.log http://127.0.0.1:8080/v1/logs
curl -s 'http://127.0.0.1:8080/v1/logs?level=ERROR&service=dispatch&from=2026-03-04T09:12:00Z&to=2026-03-04T09:14:00Z'
```

`POST /v1/logs` returns `202` after the synchronous insert succeeds. A store error returns `503`. A timed-out write can have an unknown outcome, so retries can create duplicates.

`GET /v1/logs` returns newest entries first. Filters combine with AND. `from` is inclusive and `to` is exclusive. `limit` defaults to 100 and accepts 1–500.

## Layers and checks

- `internal/logs` parses lines and defines the storage interfaces.
- `internal/httpapi` handles HTTP and validates request bounds and filters.
- `internal/clickhouse` owns the table, SQL parameters, and ClickHouse HTTP client.
- `cmd/log-ingester` starts the service and handles shutdown.

```sh
go test -race ./...
go test -race -tags integration ./...  # uses the running ClickHouse service
docker compose down --volumes        # removes the local database
```

The app listens on loopback port 8080 by default. Set `LISTEN_ADDR`, `CLICKHOUSE_URL`, `CLICKHOUSE_DB`, `CLICKHOUSE_USER`, or `CLICKHOUSE_PASSWORD` for a different local setup.
