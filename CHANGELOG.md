# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [v2.5.0] — Batch 7: traffic features (LB / health-check / port range / PROXY protocol)

Client-side traffic-layer features. All four are backward compatible: omit the new fields and behaviour is unchanged.

### Added
- **Multi-backend load balancing (F3)** — `proxies[*].local_backends: ["ip:port", ...]` with `lb_strategy: roundrobin | random | leastconn`. When empty, falls back to single `local_ip:local_port`.
- **Active health check (F4)** — `proxies[*].health_check: { enabled, type: tcp|http, interval_ms, timeout_ms, path, rise, fall }`. Background probes mark backends healthy/unhealthy; LB picker filters them with safe fallback (never black-holes).
- **Port-range proxy (F5)** — `proxies[*].remote_port_end`. A single config entry expands into N independent proxies named `<name>-<offset>` with `RemotePort=remote_port+offset`, `LocalPort=local_port+offset`. Ideal for game / P2P port batches.
- **PROXY protocol v1/v2 (F6)** — `proxies[*].proxy_protocol: v1 | v2`. Header is written to the backend immediately after dial so downstream services (nginx / haproxy) see the real client IP and port.

### Changed
- `AddProxyData` wire type gained `remote_port_end`, `local_backends`, `lb_strategy`, `proxy_protocol` so remote-add API can push the new fields too.
- `client.Client` gains `backendPools map[string]*BackendPool`, built from config at startup and updated when proxies are added at runtime.

### Notes
- These features operate on the **client** (i.e. the node that holds the local backend). The server node is unchanged.
- Health-check uses a TCP or `GET /path` probe; the backend is removed from the picker after `fall` consecutive failures and restored after `rise` consecutive successes.
- Port-range expansion is purely a client-side preprocessing step; the server observes N independent `new_proxy` registrations as usual.

## [v2.4.0] — Batch 6: hardening (auth / audit / RBAC / TLS dial)

Follow-up to v2.3.0. Covers items 3 / 5 / 6 / 7 of the post-audit roadmap.

### Added
- **`/metrics` Bearer auth** — new `node.dashboard.metrics_token`; when set, `/metrics` requires `Authorization: Bearer <token>` (or `?token=` for browsers). Constant-time comparison, returns 401 + `WWW-Authenticate` on failure. Empty value preserves backward-compat (no auth).
- **Audit log persistence** — every write op (POST/PUT/DELETE/PATCH) is appended to `data/logs/YYYY-MM-DD/audit/audit-YYYY-MM-DD.log` as JSONL (fields: time/user/role/ip/method/path/status/request_id/body). New `GET /api/v1/audit?date=&user=&path=&method=&limit=&offset=` endpoint (admin only) for querying. `node.dashboard.audit_retain_days` (default 30, 0 = forever) drives async cleanup at daily file rotation.
- **Multi-user + RBAC** — new `node.dashboard.users: [{username, password, role}]` (`role` = `admin` | `viewer`), coexisting with the legacy single `username`/`password` (treated as implicit admin). New `RequireAdmin` middleware attached to `/api/v1/*`: `viewer` may only `GET`, write methods return 403.
- **Client-side TLS dial for control channel** — `peers[*].tls`, `tls_server_name`, `tls_insecure_skip`, `tls_ca_file`. When `tls: true`, both the control TCP connection and work-conn dials switch to `tls.DialWithDialer` (TLS 1.2+). Designed to terminate at an stunnel / nginx / haproxy sidecar in front of nlink.

### Changed
- `DashboardConfig.AuthRequired()` now also returns true when `users` is non-empty.
- Session struct gains `role`; `addSession(token, username, role)`.
- Audit middleware now records `role` and writes structured JSONL via `services/audit`.

### Notes
- Server-native TLS for the gnet-based control channel is **not yet** included; it requires refactoring `tcp.Context.Conn` to an interface and is queued for a follow-up release. The current TLS dial path interoperates with any TLS terminator (stunnel/nginx/haproxy) that proxies to the plain TCP port.

## [v2.3.0] — Batch 5: user-visible features (`198258f`)

Covers the four user-visible features selected after the Batch 1-4 audit rollout.

### Added
- **Proxy edit** — `PUT /api/v1/peers/:name/proxies/:proxyName` and dashboard ✎ button on proxy cards (pre-fills the add-form, submits as update; gateway path falls back to sequential remove + add).
- **SIGHUP hot-reload** — `kill -HUP <pid>` re-reads the YAML config; `node.token` / `node.token_prev` are applied at runtime, structural changes (listen port, vpn.enabled, peers topology) are logged as "需要重启".
- **VPN policy UI** — `PUT /api/v1/vpn/peers/:vip/policy` and per-peer "编辑策略" button on the dashboard (prompt-based editor for `routes` / `allow_cidr` / `deny_cidr`); peer rows now render `virtual_ip (endpoint) routes=N rtt=Xms ↓bytes ↑bytes`.
- **e2e in CI** — new `e2e` job in `.github/workflows/ci.yml` runs `deploy/e2e/run.sh` on each push/PR, depending on `test`.

### Changed
- `TunnelService` gains `UpdatePeerProxy(connID, data)` (remove + add, errors bubble up).
- `config.ReloadConfig(path)` / `config.ApplyReload(newCfg)` added; `main.go` wait loop rewritten as a `select` over SIGINT/SIGTERM and SIGHUP.
- VPN peer list rendering on the dashboard upgraded from plain text to a rich row layout with per-peer action buttons.

## [v2.2.0] — Batch 1-4 audit rollout (`5b096b3` → `52ce1c6`)

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
