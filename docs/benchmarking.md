# Benchmarking

Run CPU-stable measurements with other heavy applications closed:

```bash
go test -run '^$' -bench=. -benchmem -count=5 ./internal/market ./internal/worker
go run ./cmd/backend
go run ./cmd/loadtest -clients 10 -events 100
go run ./cmd/loadtest -clients 100 -events 100
go run ./cmd/loadtest -clients 1000 -events 10
```

The load generator reports wall throughput, failures, and p50/p95/p99/max request latency. It labels each simulated client so per-client abuse protection remains active without treating one local IP as one client.

## Measurement notes

Results in `PROGRESS.md` are real measurements from the current machine and include OS scheduling and loopback HTTP overhead. They are not production capacity claims. The cached evaluation benchmark isolates the Minecraft critical path. The valuation benchmark intentionally measures a 500-sale recomputation, which is a background path.

The next serious performance pass should add PostgreSQL-backed `COPY` batching, a WebSocket fanout benchmark with slow consumers, snapshot encode profiling at 100k signatures, and process CPU/RSS sampling. Those require a stable container runtime and representative production data.

## 2026-08-16 local results

Windows/amd64, Intel i5-11600K, loopback HTTP:

| Measurement | Result |
| --- | ---: |
| Cached listing evaluation | 49.62 ns/op, 0 allocations |
| Existing-market observation | 1.859 µs/op, 1,137 B, 14 allocations |
| 500-sale valuation recomputation | 121.3 µs/op, 190,863 B, 28 allocations |
| 100 clients × 100 events | 9,979.8 events/s, 0 failed, p95 522.1 µs, p99 581.8 µs |
| 1,000 clients × 10 events | 4,992.9 events/s, 0 failed, p95 530.2 µs, p99 645.6 µs |

The Windows monotonic clock printed p50 as `0s` for this sub-millisecond loopback workload; treat it as below the timer resolution, not literal zero latency. Maximums were 9.21 ms and 13.69 ms respectively. These are single-machine demo measurements, not production capacity guarantees.
