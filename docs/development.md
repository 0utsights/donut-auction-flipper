# Development

## Toolchains

- Go 1.26+
- Node.js 22.13+ and npm
- Docker Desktop for the complete environment
- JDK 21 and Gradle for the Fabric module

Copy `.env.example` to `.env` only when local environment loading is configured; the programs also accept ordinary process environment variables. Never commit credentials.

## Common loop

```bash
gofmt -w cmd internal
go vet ./...
go test -race ./...
cd apps/dashboard && npm ci && npm run lint && npm test
docker compose config
docker compose up --build
```

Default Compose startup contains no generated listings, transactions, workers, flips, or purchases. Use `docker compose --profile live up --build` with `DONUT_API_KEY` set to run the official API collector. The `simulation` profile exists only for explicit testing and requires `DN_DATA_MODE=simulation` so test records cannot enter a live-mode backend.

The dashboard shows a truthful offline empty state if the backend is unavailable. Its same-origin server route reads `DN_BACKEND_URL` and forwards the server-only `DN_ADMIN_TOKEN`; the credential is never embedded in browser JavaScript. Market-critical clients use WebSockets; dashboard polling is intentionally outside that path.

## Adding a realtime message

1. Add its numeric type without renumbering existing types.
2. Choose the lowest priority justified by latency needs.
3. Add validation and size bounds in the server handler.
4. Add a protocol round-trip test and a priority/backpressure test if behavior changes.
5. Update `docs/protocol.md`.

## Database changes

Add an ordered migration under `infra/migrations`; never edit an applied production migration. Check hot queries with `EXPLAIN (ANALYZE, BUFFERS)` against production-like row counts. Observation and transaction partitions need a scheduled partition/retention job before production.
