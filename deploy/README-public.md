# NLink

P2P TCP / UDP 隧道工具：单二进制、配置驱动，每个节点既能监听也能主动连，同时支持 **TCP/UDP 端口转发** 和 **VPN 虚拟局域网（TUN + UDP 加密隧道）**。

| 概览 | 网络拓扑 |
|------|---------|
| ![overview](https://github.com/hsqbyte/nlink/releases/latest/download/screenshot-overview.png) | ![network](https://github.com/hsqbyte/nlink/releases/latest/download/screenshot-network.png) |

## 特性

- **对等架构** — 没有固定的"服务端/客户端"，按配置决定角色
- **TCP / UDP / HTTP 端口转发** — 把内网服务暴露到公网
- **VPN 虚拟局域网** — 节点间用 `10.0.0.x` 直接互通（ping / ssh / 任意 TCP-UDP）
- **NAT 穿透** — 内置 STUN + UDP 打洞，两端都在 NAT 后也能直连
- **可视化 Dashboard** — 实时拓扑图、流量统计、点击节点直接配置代理
- **AES-256-GCM 加密** + Token 认证
- **断线自动重连** + 心跳保活
- **跨平台**：Linux / macOS / Windows × amd64 / arm64

## 安装

### 一键安装（推荐）

**Linux / macOS** — 默认装到 `/opt/nlink`：

```bash
curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.sh | sudo bash
```

**Windows**（管理员 PowerShell，Win10 1803+）— 默认装到 `C:\nlink`：

```powershell
[Console]::OutputEncoding=[Text.Encoding]::UTF8; iex (curl.exe -sL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.ps1 | Out-String)
```

国内 GitHub 慢的话脚本会自动 fallback 到几个公开镜像；也可以手动指定：

```bash
curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.sh \
  | sudo MIRROR=https://gh-proxy.com bash
```

安装脚本会跑一个交互向导，问几个问题（节点名 / token / 是否监听 / VPN / Dashboard）后自动生成 `config/nlink.yaml` 并启动。

## 运维

`install.sh` 安装时把 `admin.sh` 挂到全局 `nlink-admin`，日常一个命令搞定：

```bash
sudo nlink-admin start          # 启动
sudo nlink-admin stop           # 停止
sudo nlink-admin restart        # 重启
sudo nlink-admin reload         # 热重载配置（不断连接）
sudo nlink-admin status         # 看状态
sudo nlink-admin logs -f        # 持续跟日志
sudo nlink-admin update         # 升级到最新版
sudo nlink-admin update v2.9.7  # 升到指定版本
sudo nlink-admin version        # 看 nlink 版本
```

自动识别 systemd / nohup 模式，无需关心底层用的是 `systemctl` 还是 `pkill`。

## 手动下载

不想跑脚本就去 [Releases](https://github.com/hsqbyte/nlink/releases/latest) 下：

| 平台 | 资产 |
|------|------|
| Linux x86_64 / ARM64 | `nlink-linux-{amd64,arm64}.tar.gz` |
| macOS Intel / Apple Silicon | `nlink-darwin-{amd64,arm64}.tar.gz` |
| Windows x64 / ARM64 / 32-bit | `nlink-windows-{amd64,arm64,386}.zip` |

每个压缩包含：`nlink` 二进制 + `update.sh` + `admin.sh` + `nlink.service` + `INSTALL.txt`。

校验：

```bash
sha256sum -c SHA256SUMS.txt --ignore-missing
```

## 反馈 / 问题

在 [Issues](https://github.com/hsqbyte/nlink/issues) 里反馈。
