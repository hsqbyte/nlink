#!/usr/bin/env bash
# nlink 一键更新脚本
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/update.sh | sudo bash
#   或本地执行:
#     sudo ./update.sh              # 拉取最新 release
#     sudo ./update.sh v2.9.0       # 指定版本
#
# 环境变量（可选）:
#   INSTALL_DIR   nlink 安装目录   (默认 /opt/nlink)
#   CONFIG_PATH   配置文件         (默认 $INSTALL_DIR/config/nlink.yaml)
#   LOG_PATH      非 systemd 模式下的日志文件 (默认 $INSTALL_DIR/nlink.log)
#
# 兼容场景:
#   - systemd 服务 (服务名 nlink)
#   - nohup / 前台进程 (按二进制路径 pkill 后用 nohup 重启)

set -euo pipefail

VERSION="${1:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/opt/nlink}"
BIN="$INSTALL_DIR/nlink"
CONFIG="${CONFIG_PATH:-$INSTALL_DIR/config/nlink.yaml}"
LOG="${LOG_PATH:-$INSTALL_DIR/nlink.log}"
REPO="hsqbyte/nlink"

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
info()   { printf '→ %s\n' "$*"; }

# ---- 平台检测 ----
case "$(uname -s)" in
  Linux)  OS=linux ;;
  Darwin) OS=darwin ;;
  *) red "不支持的 OS: $(uname -s)"; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) red "不支持的架构: $(uname -m)"; exit 1 ;;
esac
ASSET="nlink-${OS}-${ARCH}.tar.gz"

# ---- 下载 URL ----
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

# 国内 / GitHub 慢的环境，可指定镜像前缀（如 https://ghproxy.cn）
if [ -n "${MIRROR:-}" ]; then
  URL="${MIRROR%/}/${URL}"
fi

info "目标版本: $VERSION ($ASSET)"
info "安装目录: $INSTALL_DIR"

if [ ! -d "$INSTALL_DIR" ]; then
  info "目录不存在，创建 $INSTALL_DIR"
  mkdir -p "$INSTALL_DIR"
fi

# ---- 下载 + 解包 ----
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT
TARBALL="$TMPDIR/nlink.tar.gz"

# 尝试列表：MIRROR → 直连 → 公开镜像
build_urls() {
  if [ "$VERSION" = "latest" ]; then
    RAW="https://github.com/${REPO}/releases/latest/download/${ASSET}"
  else
    RAW="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  fi
  if [ -n "${MIRROR:-}" ]; then
    echo "${MIRROR%/}/${RAW}"
  fi
  echo "$RAW"
  echo "https://github.moeyy.xyz/${RAW}"
  echo "https://gh-proxy.com/${RAW}"
  echo "https://mirror.ghproxy.com/${RAW}"
  echo "https://hub.gitmirror.com/${RAW}"
  echo "https://gh.idayer.com/${RAW}"
}

DOWNLOAD_OK=0
for u in $(build_urls); do
  info "尝试：$u"
  if curl -fL --retry 2 --connect-timeout 10 --speed-time 30 --speed-limit 1024 \
         --progress-bar -o "$TARBALL" "$u"; then
    DOWNLOAD_OK=1
    break
  fi
  yellow "  失败/超时，尝试下一个镜像..."
done

if [ "$DOWNLOAD_OK" != "1" ]; then
  red "全部下载源都失败"
  exit 1
fi

info "解包"
tar -xzf "$TARBALL" -C "$TMPDIR"
EXTRACTED_DIR="$TMPDIR/nlink-${OS}-${ARCH}"
if [ ! -d "$EXTRACTED_DIR" ]; then
  red "压缩包结构异常：找不到 $EXTRACTED_DIR"
  exit 1
fi

NEW_BIN="$EXTRACTED_DIR/nlink"
if [ ! -x "$NEW_BIN" ]; then
  chmod +x "$NEW_BIN" 2>/dev/null || true
fi
if ! file "$NEW_BIN" >/dev/null 2>&1 || ! file "$NEW_BIN" | grep -qiE 'executable|mach-o'; then
  red "压缩包里的 nlink 不是可执行二进制"
  exit 1
fi

# ---- 检测运行模式 ----
USE_SYSTEMD=0
if command -v systemctl >/dev/null 2>&1 && systemctl list-unit-files --no-pager 2>/dev/null | grep -q '^nlink\.service'; then
  USE_SYSTEMD=1
  info "检测到 systemd 服务 nlink"
fi

# ---- 停止旧进程 ----
if [ "$USE_SYSTEMD" = "1" ]; then
  info "停止 systemd 服务"
  systemctl stop nlink || true
else
  if pgrep -f "$BIN" >/dev/null 2>&1; then
    info "停止旧进程"
    pkill -TERM -f "$BIN" || true
    # 等 3 秒优雅退出，超时则 KILL
    for i in 1 2 3 4 5 6; do
      pgrep -f "$BIN" >/dev/null 2>&1 || break
      sleep 0.5
    done
    if pgrep -f "$BIN" >/dev/null 2>&1; then
      red "进程没在 3 秒内优雅退出，强制 KILL"
      pkill -KILL -f "$BIN" || true
    fi
  fi
fi

# ---- 备份 + 替换 ----
if [ -f "$BIN" ]; then
  STAMP="$(date +%Y%m%d-%H%M%S)"
  cp -f "$BIN" "$BIN.bak.$STAMP"
  info "备份旧二进制 → $BIN.bak.$STAMP"
fi
install -m 0755 "$NEW_BIN" "$BIN"

# 顺便把压缩包里的 update.sh / admin.sh / nlink.service 也同步过来（如果存在）
# 这样下次升级会用到改进过的脚本和 systemd unit
if [ -f "$EXTRACTED_DIR/update.sh" ]; then
  install -m 0755 "$EXTRACTED_DIR/update.sh" "$INSTALL_DIR/update.sh"
fi
if [ -f "$EXTRACTED_DIR/admin.sh" ]; then
  install -m 0755 "$EXTRACTED_DIR/admin.sh" "$INSTALL_DIR/admin.sh"
  ln -sf "$INSTALL_DIR/admin.sh" /usr/local/bin/nlink-admin 2>/dev/null || true
fi
mkdir -p "$INSTALL_DIR/deploy"
if [ -f "$EXTRACTED_DIR/nlink.service" ]; then
  install -m 0644 "$EXTRACTED_DIR/nlink.service" "$INSTALL_DIR/deploy/nlink.service"
fi

trap - EXIT
rm -rf "$TMPDIR"

# ---- 启动 ----
if [ "$USE_SYSTEMD" = "1" ]; then
  info "启动 systemd 服务"
  systemctl start nlink
  sleep 1
  systemctl --no-pager status nlink | sed 's/^/  /' | head -n 12
else
  if [ ! -f "$CONFIG" ]; then
    red "配置文件不存在: $CONFIG"
    exit 1
  fi
  info "启动 (nohup)"
  cd "$INSTALL_DIR"
  nohup "$BIN" -c "$CONFIG" >> "$LOG" 2>&1 &
  sleep 1
  if pgrep -fa "$BIN"; then
    :
  else
    red "进程未起来，查看日志 $LOG"
    tail -n 30 "$LOG" || true
    exit 1
  fi
fi

green "✓ 更新完成"
