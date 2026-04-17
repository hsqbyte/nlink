# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

This block documents the four-batch audit-driven rollout completed in the current development cycle.
Covers commits `5b096b3` → `52ce1c6`.

### Added

#### Batch 1 — Security hardening / CI / Observability (`5b096b3`)
- AES-256-GCM over UDP for the VPN transport (HKDF-derived key, nonce = 8B random prefix + 4B atomic counter), with replay protection.
- Token rotation support on the node: `token_prev` accepted during grace period.
- HTTPS on the dashboard when `tls_cert_file` + `tls_key_file` are configured.
- CORS whitelist + security headers middleware (`X-Content-Type-Options`, `X-Frame-Options`, `Referrer-Policy`).
- Prometheus scrape endpoint at `GET /metrics` with process, proxy, peer and VPN gauges/counters.
- GitHub Actions CI: `go vet`, `go test -race`, `gosec`, multi-OS build matrix.

#### Batch 2 — Observability / Realtime / UX / Packaging (`bcb7d3c`)
- Server-Sent Events stream at `GET /api/v1/stream` pushing live `stats` snapshots to the dashboard.
- Historical traffic rings and `/api/v1/stats/history` for dashboard time-series charts.
- Dark-mode CSS and responsive tweaks for the dashboard.
- Multi-stage `Dockerfile` + `docker-compose.yml` under `deploy/docker/`.
- Structured log rotation to `data/logs/YYYY-MM-DD/app/`.

#### Batch 3 — Proxy policy (`34132f8`)
- ACL on proxies: `allow_cidr` / `deny_cidr` per proxy, enforced at accept time.
- Per-connection rate limiting (`rate_limit` bytes/sec) backed by `golang.org/x/time/rate`.
- UDP proxy type (associates client→backend sessions, keyed by source address).
- HTTP reverse proxy type with `custom_domains` virtual-host routing and optional `host_rewrite`, sharing `vhost_http_port`.

#### Batch 4 — VPN policy / API audit / UI / e2e (`4a266be` + `52ce1c6`)
- **VPN subnet routing (D1)**: `UDPPeer.Routes []*net.IPNet`, longest-prefix-match lookup (`LookupPeerForDst`), and tunToUDP fallback when destination is outside the local subnet.
- **VPN ACL (D2)**: `AllowNets` / `DenyNets` per peer; `PeerACLAllowsPacket(src,dst)` applied on both `SendTo` and `RecvFrom`; rejected packets counted in `Rx/TxDropped`.
- **VPN metrics (D3)**: atomic `RxBytes/TxBytes/RxPackets/TxPackets/Rx/TxDropped` per peer, periodic encrypted RTT probe (control frames `0xFE` / `0xFF`, 13 B payload), background loop every 30 s. Metrics exposed via `GET /api/v1/vpn/peers` including `rtt_ms`.
- **Request ID + structured access log (A3)**: `X-Request-ID` honoured/emitted; per-request log with method/path/status/latency/ip/user.
- **API audit log (B3)**: write ops (POST/PUT/DELETE/PATCH) captured with body snippet (≤200 B), skipping `/login`. Sessions now carry `username` so audit entries identify the actor.
- **Dashboard policy UI (E3)**: "Add proxy" form now supports `type` (tcp/udp/http) with conditional fields (`custom_domains` vs `remote_port`), `allow_cidr`, `deny_cidr`, `rate_limit`. Proxy cards render type badges and ACL/rate summary.
- **End-to-end smoke test (F3)**: `deploy/e2e/docker-compose.yml` spins up nlink-server + echo backend + nlink-client; `deploy/e2e/run.sh` builds the image, waits for `/health`, confirms the remote proxy port is listening, and round-trips a payload through the tunnel. Verified locally: `✅ PASS`.

### Changed
- `PeerConfig` gains `VPNRoutes`, `VPNAllowCIDR`, `VPNDenyCIDR`.
- `ProxyConfig` gains `CustomDomains`, `HostRewrite`, `AllowCIDR`, `DenyCIDR`, `RateLimit`.
- Gin middleware chain replaced `gin.Logger()` with `RequestID` → `AccessLog` → `Recovery` → `SecurityHeaders` → `CORS` → `Audit`.
- `addSession(token)` → `addSession(token, username)`; new helper `SessionUsername(token)`.

### Fixed
- Cached STUN public address to avoid blocking the stats API (`20303dc`).
- Duplicate `package handle` declaration in `metrics_test.go` that was blocking the test package.
- `metrics_test.go` data race warnings when using Prometheus default registry in parallel tests.

### Security
- UDP VPN frames now authenticated (AES-GCM) with replay protection.
- Dashboard supports HTTPS; CORS locked down to a configurable whitelist.
- Audit trail for every mutating API call.

### Known limitations
- VPN inside containers needs `cap_add: [NET_ADMIN]` + `devices: [/dev/net/tun]`; the e2e compose intentionally disables VPN to stay portable.
- Dashboard currently only supports *adding* and *removing* proxies; editing requires delete + re-add.
