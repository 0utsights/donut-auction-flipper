# Realtime protocol

WebSocket messages are binary. A frame is:

| Offset | Size | Meaning |
| --- | ---: | --- |
| 0 | 1 | protocol version (`1`) |
| 1 | 1 | priority (`0=P0`, `1=P1`, `2=P2`) |
| 2 | 1 | message type |
| 3 | 4 | big-endian payload length |
| 7 | N | JSON payload, maximum 64 KiB |

JSON payloads keep the reference implementation debuggable while the envelope locks down framing and priority. A later protobuf migration can replace payloads without changing transport scheduling.

P0 carries price invalidations, recovery snapshots, and assignments. P1 carries observations, price updates, flips, purchase results and heartbeats. P2 carries chat, presence and analytics. A full recovery snapshot that exceeds 64 KiB is sorted and split into `SnapshotChunk` frames of 32 valuations; no oversize encode failure is silently broadcast. Each client has a 256-frame bounded queue: P2 is evicted to admit market-critical traffic.

On connect, the server authenticates the development token from the Authorization header (with a browser-only query fallback), validates the Origin when present, sends a full or chunked snapshot, and begins ping/pong liveness checks. Native Fabric 0.3 uses the separate compact HTTP snapshot endpoint with ETags; WebSocket recovery remains available to network workers.

Inbound frames are limited to 64 KiB. Each session is capped at 2,000 frames/10 seconds and 20 chat frames/10 seconds. Chat text is capped at 500 characters and channel names are allow-listed.

Assignments are delivered only to their selected worker. Slow consumers count dropped frames; a full market-critical queue disconnects that consumer so it must recover from a new full snapshot. Worker presence and assignments are removed when the final connection for a worker closes.
