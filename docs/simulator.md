# Simulator

This is an opt-in test fixture. It is never part of the normal runtime and its output must not be treated as live Donut market information. It seeds transaction history, connects test WebSocket workers, and generates listings solely for deterministic development, load, and failure testing.

```bash
go run ./cmd/simulator -backend http://localhost:8080 -rate 50 -workers 8 -flip-percent 8 -history 80
DN_DATA_MODE=simulation docker compose --profile simulation up --build
```

Every generated slot observation is evaluated against the worker’s local immutable cache before observation telemetry is sent. The separate market-ingest feed is batched every 500 ms so high event rates remain below HTTP request limits. Simulated listings expire after 30 seconds. Purchases use the simulator controller and are revalidated. `Ctrl+C` prints actual generated, observed, flip, purchase and throughput totals.

Search-sharding comparison uses worker count, item count and event rate to contrast a broad scanner’s queueing latency with specialized scanners. It is a transparent capacity model, not a claim about live Donut timing.
