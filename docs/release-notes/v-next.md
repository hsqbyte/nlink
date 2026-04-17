# nlink — Audit rollout (Batch 1-4)

This release delivers the four-batch hardening & feature rollout tracked internally
as the audit roadmap (`5b096b3` → `52ce1c6`).

## Highlights

- 🔐 **VPN transport is now authenticated & encrypted** (AES-256-GCM + replay window).
- 🧭 **VPN subnet routing + per-peer ACL + metrics/RTT** exposed on `/api/v1/vpn/peers`.
- 🚦 **Proxy policy**: ACL (`allow_cidr`/`deny_cidr`), per-connection rate-limit, UDP type, HTTP virtual-host type.
- 📊 **Observability**: Prometheus `/metrics`, SSE stream `/api/v1/stream`, traffic history, dark mode.
- 🧾 **API audit**: `X-Request-ID`, structured access log, body-snippet audit on writes, session username tracking.
- 🎛️ **Dashboard**: policy fields in the add-proxy form (type/ACL/rate), type badges on cards.
- 🧪 **End-to-end test**: `deploy/e2e/run.sh` builds Docker image, brings up a two-node + echo stack, round-trips traffic.
- 🧰 **CI**: vet / race tests / gosec / multi-OS build.

## Upgrade notes

- **Config additions** (all optional, defaults unchanged):
  - `peers[*].vpn_routes`, `vpn_allow_cidr`, `vpn_deny_cidr`
  - `peers[*].proxies[*].{custom_domains, host_rewrite, allow_cidr, deny_cidr, rate_limit}`
  - `node.token_prev` for rotation
  - `node.dashboard.{tls_cert_file, tls_key_file}` for HTTPS
  - `node.listen.vhost_http_port` for HTTP reverse-proxy sharing
- **Breaking**: none. Existing configs load unchanged.
- **Runtime**: VPN inside containers requires `cap_add: [NET_ADMIN]` + `devices: [/dev/net/tun]`.

## Verifying locally

```bash
./deploy/e2e/run.sh
# expect: [e2e] ✅ PASS: round-trip through proxy succeeded
```

See [CHANGELOG.md](CHANGELOG.md) for the itemized list.
