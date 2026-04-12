# NLink

English | [中文](README.md)

Lightweight P2P TCP tunneling tool. Single binary, config-driven — every instance can accept connections, connect to other nodes, or both as a relay.

## Features

- **Peer-to-Peer Architecture** — No fixed "server/client"; each node's role is determined by config
- **Single Binary** — One `nlink` for all scenarios
- **TCP Port Forwarding** — Map any internal TCP service to a remote port
- **Visual Dashboard** — Built-in web management panel with vis-network topology graph, real-time node/proxy status
- **Connection Pooling** — Pre-built work connections to reduce first-request latency
- **Remote Management** — Manage peer node proxies and connection pools via Dashboard
- **Multi-Level Tunneling** — Supports A → B → C chain forwarding with cross-level management
- **Auto Reconnect** — Exponential backoff reconnection on disconnect
- **Heartbeat** — Automatic node liveness detection
- **Token Auth** — Secure node authentication

## How It Works

```
User ──▶ :9080 ──▶ Node-A(listen) ══tunnel══▶ Node-B(peer) ──▶ 127.0.0.1:8080
```

Each node can configure:

| Config Block | Purpose |
|-------------|---------|
| `node.listen` | Accept connections from other nodes (TCP control + work channels) |
| `node.dashboard` | Enable web management panel |
| `peers` | Actively connect to other nodes and register proxies |

## Installation

### Download from Releases

Go to [Releases](https://github.com/hsqbyte/nlink/releases) to download the binary for your platform.

### Build from Source

```bash
git clone https://github.com/hsqbyte/nlink.git
cd nlink
go build -o nlink .
```

## Quick Start

### Node A — Public machine, accepts connections

```yaml
# config/nlink.yaml
node:
  name: "node-a"
  token: "your-secret"
  listen:
    port: 7000
    pool_count: 5
  dashboard:
    port: 18080
```

### Node B — Internal machine, connects to A

```yaml
# config/node-b.yaml
node:
  name: "node-b"
  token: "your-secret"
  dashboard:
    port: 18081

peers:
  - addr: "x.x.x.x"
    port: 7000
    token: "your-secret"
    pool_count: 2
    proxies:
      - name: "web"
        type: "tcp"
        local_ip: "127.0.0.1"
        local_port: 8080
        remote_port: 9080
```

### Run

```bash
# Node A
nlink -c config/nlink.yaml

# Node B
nlink -c config/node-b.yaml
```

Visit `http://node-a:18080` or `http://node-b:18081` for the Dashboard.

External users can reach node-b's `127.0.0.1:8080` via `node-a:9080`.

## CLI Options

```
nlink [options]

  -c string          Config file path (default "config/nlink.yaml")
  -dashboard         Force enable dashboard (even without dashboard config)
  -dashboard-port    Dashboard port (used with -dashboard, default 18080)
```

## Full Configuration Reference

```yaml
node:
  name: "node-a"                    # Node name (unique identifier)
  token: "your-secret"              # Auth token

  listen:                           # Optional — accept connections from other nodes
    port: 7000                      #   Control channel port (work conn = port+1)
    max_message_size: 65536         #   Max message size (bytes)
    heartbeat_timeout: 90           #   Heartbeat timeout (seconds)
    max_proxies_per_peer: 10        #   Max proxies per peer
    work_conn_timeout: 10           #   Work connection timeout (seconds)
    pool_count: 5                   #   Global connection pool size (0=disabled)

  dashboard:                        # Optional — web management panel
    enabled: true                   #   Enable/disable (default true)
    port: 18080                     #   HTTP port
    shutdown_timeout: 30            #   Graceful shutdown timeout (seconds)
    username: "admin"               #   Login username (empty = no auth)
    password: "admin"               #   Login password

peers:                              # Optional — nodes to actively connect to
  - addr: "192.168.1.100"
    port: 7000
    token: "your-secret"
    pool_count: 5
    proxies:
      - name: "web"
        type: "tcp"
        local_ip: "127.0.0.1"
        local_port: 8080
        remote_port: 9080
```

## API

| Method | Path | Description |
|--------|------|-------------|
| GET | /api/v1/stats | Dashboard statistics |
| GET | /api/v1/proxies | List all proxies |
| GET | /api/v1/peers | List online peers |
| GET | /api/v1/status | Node status |
| GET | /api/v1/node/config | Get node config |
| PUT | /api/v1/node/config | Hot-update config |
| DELETE | /api/v1/peers/:name | Kick peer |
| GET | /api/v1/peers/:name/config | Get peer config |
| POST | /api/v1/peers/:name/proxies | Remote add proxy |
| DELETE | /api/v1/peers/:name/proxies/:proxy | Remote remove proxy |
| PUT | /api/v1/peers/:name/pool | Update peer connection pool |

## License

[GPL-3.0](LICENSE)
