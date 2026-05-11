#!/usr/bin/env bash
# nlink 一键安装脚本 (Linux / macOS)
#
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.sh | sudo bash
#
# 自定义:
#   curl ... | sudo VERSION=v2.9.4 INSTALL_DIR=/opt/nlink bash
#
# 环境变量:
#   VERSION       要安装的版本 (默认 latest)
#   INSTALL_DIR   安装目录     (默认 /opt/nlink)
#   ENABLE_SYSTEMD  =1 时安装并 enable systemd 服务（默认只安装单元文件，不自启）

set -euo pipefail

VERSION="${VERSION:-latest}"
INSTALL_DIR="${INSTALL_DIR:-/opt/nlink}"
REPO="hsqbyte/nlink"

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
info()   { printf '→ %s\n' "$*"; }
hr()     { printf '\033[36m%s\033[0m\n' "────────────────────────────────────────────────────────────"; }

# ---- 权限检查 ----
if [ "$(id -u)" -ne 0 ]; then
  red "请用 root 运行:  curl ... | sudo bash"
  exit 1
fi

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

hr
green "nlink 一键安装"
echo "  版本:     $VERSION"
echo "  平台:     ${OS}/${ARCH}"
echo "  安装到:   $INSTALL_DIR"
hr

# ---- 下载 URL ----
if [ "$VERSION" = "latest" ]; then
  URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"
else
  URL="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
fi

# 国内 / GitHub 慢的环境，可指定镜像前缀（如 https://ghproxy.cn）
# 例: curl ... | sudo MIRROR=https://ghproxy.cn bash
if [ -n "${MIRROR:-}" ]; then
  URL="${MIRROR%/}/${URL}"
fi

# ---- 准备目录 ----
mkdir -p "$INSTALL_DIR" "$INSTALL_DIR/config" "$INSTALL_DIR/deploy" "$INSTALL_DIR/data"

# ---- 下载 + 解包 ----
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT
TARBALL="$TMPDIR/nlink.tar.gz"

# 尝试列表：用户指定的 MIRROR 排第一，然后 fallback 一串公开镜像，最后是直连
# 镜像随时可能挂，按"近期可用"顺序排
build_urls() {
  if [ "$VERSION" = "latest" ]; then
    RAW="https://github.com/${REPO}/releases/latest/download/${ASSET}"
  else
    RAW="https://github.com/${REPO}/releases/download/${VERSION}/${ASSET}"
  fi
  # 1. 用户指定的 MIRROR（如果有）
  if [ -n "${MIRROR:-}" ]; then
    echo "${MIRROR%/}/${RAW}"
  fi
  # 2. 直连 GitHub
  echo "$RAW"
  # 3. 公开镜像
  echo "https://github.moeyy.xyz/${RAW}"
  echo "https://gh-proxy.com/${RAW}"
  echo "https://mirror.ghproxy.com/${RAW}"
  echo "https://hub.gitmirror.com/${RAW}"
  echo "https://gh.idayer.com/${RAW}"
}

info "下载 $URL"
DOWNLOAD_OK=0
for u in $(build_urls); do
  info "尝试：$u"
  # 10 秒连接超时 + 5 秒总头响应；进度条显示
  if curl -fL --retry 2 --connect-timeout 10 --speed-time 30 --speed-limit 1024 \
         --progress-bar -o "$TARBALL" "$u"; then
    DOWNLOAD_OK=1
    break
  fi
  yellow "  失败/超时，尝试下一个镜像..."
done

if [ "$DOWNLOAD_OK" != "1" ]; then
  red "全部下载源都失败。可手动指定: MIRROR=<你能访问的代理前缀> 再跑一次"
  red "或者: VERSION=v2.9.5 直接下:"
  red "  wget https://github.com/${REPO}/releases/download/v2.9.5/${ASSET}"
  exit 1
fi

info "解包"
tar -xzf "$TARBALL" -C "$TMPDIR"
EXTRACTED="$TMPDIR/nlink-${OS}-${ARCH}"
if [ ! -d "$EXTRACTED" ]; then
  red "压缩包结构异常"
  exit 1
fi

# ---- 安装文件 ----
# 已有进程的话先停（沿用 update.sh 同款逻辑）
if pgrep -f "$INSTALL_DIR/nlink" >/dev/null 2>&1; then
  info "停止运行中的 nlink"
  if command -v systemctl >/dev/null 2>&1 && systemctl is-active --quiet nlink 2>/dev/null; then
    systemctl stop nlink || true
  else
    pkill -TERM -f "$INSTALL_DIR/nlink" || true
    sleep 2
    pkill -KILL -f "$INSTALL_DIR/nlink" 2>/dev/null || true
  fi
fi

# 备份旧二进制
if [ -f "$INSTALL_DIR/nlink" ]; then
  STAMP="$(date +%Y%m%d-%H%M%S)"
  cp -f "$INSTALL_DIR/nlink" "$INSTALL_DIR/nlink.bak.$STAMP"
  info "已备份旧二进制 → nlink.bak.$STAMP"
fi

info "安装二进制 + 脚本"
install -m 0755 "$EXTRACTED/nlink"        "$INSTALL_DIR/nlink"
install -m 0755 "$EXTRACTED/update.sh"    "$INSTALL_DIR/update.sh"
if [ -f "$EXTRACTED/admin.sh" ]; then
  install -m 0755 "$EXTRACTED/admin.sh"   "$INSTALL_DIR/admin.sh"
  # 顺手挂个 /usr/local/bin/nlink-admin 软链，全局可用（如果有权限）
  ln -sf "$INSTALL_DIR/admin.sh" /usr/local/bin/nlink-admin 2>/dev/null || true
fi
install -m 0644 "$EXTRACTED/nlink.service" "$INSTALL_DIR/deploy/nlink.service"
install -m 0644 "$EXTRACTED/README.md"    "$INSTALL_DIR/README.md" 2>/dev/null || true
install -m 0644 "$EXTRACTED/INSTALL.txt"  "$INSTALL_DIR/INSTALL.txt" 2>/dev/null || true
install -m 0644 "$EXTRACTED/LICENSE"      "$INSTALL_DIR/LICENSE" 2>/dev/null || true

# ---- systemd 单元（先装，wizard 完才决定是否 enable） ----
SVC_INSTALLED=0
if command -v systemctl >/dev/null 2>&1 && [ -d /etc/systemd/system ]; then
  info "安装 systemd 单元到 /etc/systemd/system/nlink.service"
  sed "s|/opt/nlink|${INSTALL_DIR}|g" \
    "$INSTALL_DIR/deploy/nlink.service" > /etc/systemd/system/nlink.service
  systemctl daemon-reload
  SVC_INSTALLED=1
fi

trap - EXIT
rm -rf "$TMPDIR"

# ---- 配置向导 ----
CONFIG_FILE="$INSTALL_DIR/config/nlink.yaml"

# 交互可用性：曲径管道执行时 stdin 是 pipe，要从 /dev/tty 读
INTERACTIVE=0
if [ "${SKIP_WIZARD:-0}" != "1" ] && [ -r /dev/tty ] && [ -w /dev/tty ]; then
  INTERACTIVE=1
fi

# 直接赋值到变量，避免 $(...) 子 shell + /dev/tty 重定向组合的玄学
# 用法: ask VARNAME "提示" "默认值"
ask() {
  local _var="$1" _prompt="$2" _default="${3:-}" _ans=""
  local _full
  if [ -n "$_default" ]; then
    _full="  $_prompt [$_default]: "
  else
    _full="  $_prompt: "
  fi
  # -p 把 prompt 打到 stderr (终端)，read 从 /dev/tty 拿用户输入
  IFS= read -r -p "$_full" _ans </dev/tty || _ans=""
  _ans="${_ans:-$_default}"
  printf -v "$_var" '%s' "$_ans"
}

# 用法: ask_required VARNAME "提示"
ask_required() {
  local _var="$1" _prompt="$2" _ans=""
  while [ -z "$_ans" ]; do
    IFS= read -r -p "  $_prompt: " _ans </dev/tty || _ans=""
    [ -z "$_ans" ] && printf '\033[31m    必填\033[0m\n' >&2
  done
  printf -v "$_var" '%s' "$_ans"
}

# 用法: ask_yn VARNAME "提示" "y"/"n"(默认) → 变量值为 "y" / "n"
ask_yn() {
  local _var="$1" _prompt="$2" _default="$3" _hint _ans
  if [ "$_default" = "y" ]; then _hint="Y/n"; else _hint="y/N"; fi
  while :; do
    IFS= read -r -p "  $_prompt ($_hint): " _ans </dev/tty || _ans=""
    _ans="${_ans:-$_default}"
    case "$_ans" in
      [Yy]|[Yy][Ee][Ss]) printf -v "$_var" '%s' "y"; return ;;
      [Nn]|[Nn][Oo])     printf -v "$_var" '%s' "n"; return ;;
      *) printf '    请输入 y 或 n\n' >&2 ;;
    esac
  done
}

gen_token() {
  if command -v openssl >/dev/null 2>&1; then
    openssl rand -hex 16
  else
    LC_ALL=C tr -dc 'A-Za-z0-9' < /dev/urandom | head -c 32
    echo
  fi
}

NLINK_STARTED=0

if [ -f "$CONFIG_FILE" ]; then
  yellow "已存在配置 $CONFIG_FILE，保留不动（跳过向导）"
elif [ "$INTERACTIVE" != "1" ]; then
  yellow "非交互环境，写入示例配置 $CONFIG_FILE（含占位符，需手动编辑）"
  cat > "$CONFIG_FILE" <<'YAML'
node:
  name: "nlink-server"
  token: "CHANGE-ME"
  listen:
    port: 7100
    pool_count: 5
  dashboard:
    enabled: true
    port: 18080
    username: "admin"
    password: "CHANGE-ME"
YAML
else
  # 向导里很多命令（hostname / head -c / 短路 &&）跟 strict mode 不兼容；
  # 关掉 errexit / pipefail 走交互流程，结束再恢复
  set +e
  set +o pipefail

  hr
  green "首次配置向导"
  echo "（按回车采用默认值；输错可以最后再 sudo vi $CONFIG_FILE 改）" > /dev/tty

  echo "nlink 是对等的：每个节点都可以同时'被别人连'和'主动连别人'，按需勾选即可。"
  echo

  # ---- 节点身份 ----
  DEFAULT_NAME="nlink-$(hostname 2>/dev/null | tr -d ' ' | head -c 24 || true)"
  if [ -z "$DEFAULT_NAME" ] || [ "$DEFAULT_NAME" = "nlink-" ]; then
    DEFAULT_NAME="nlink-node"
  fi
  ask NODE_NAME "节点名（全网唯一）" "$DEFAULT_NAME"

  AUTO_TOKEN="$(gen_token)"
  echo "  通信 token（多个互连节点必须用同一个；回车=自动生成强随机）"
  echo "  自动生成: $AUTO_TOKEN"
  ask TOKEN "token" "$AUTO_TOKEN"

  echo

  # ---- 监听 ----
  ask_yn ENABLE_LISTEN "接受其他节点连进来（开 TCP 监听端口）？" "y"
  if [ "$ENABLE_LISTEN" = "y" ]; then
    ask LISTEN_PORT "TCP 监听端口" "7100"
  fi

  # ---- VPN ----
  echo
  ask_yn ENABLE_VPN "启用 VPN 虚拟局域网（节点间 10.0.0.x 直接互通）？" "y"
  if [ "$ENABLE_VPN" = "y" ]; then
    echo "  本节点的虚拟 IP（CIDR 形式）"
    echo "  - 全网第一个节点用 10.0.0.1/24"
    echo "  - 后续节点用 10.0.0.2/24、10.0.0.3/24 …（自己挑不冲突的）"
    echo "  - 或填 auto，启动时向已有节点请求 DHCP 分配"
    ask VPN_VIP "本节点虚拟 IP" "10.0.0.1/24"
    ask VPN_PORT "VPN UDP 端口" "7200"
  fi

  # ---- 主动连接 ----
  echo
  ask_yn ENABLE_PEER "现在要主动连接另一个 nlink 节点吗？" "n"
  if [ "$ENABLE_PEER" = "y" ]; then
    echo "  注意：对端 token 必须跟本节点一致；如果你刚改了 token，对端也得同步"
    ask_required PEER_ADDR "对端公网 IP / 域名"
    ask PEER_PORT "对端 TCP 端口" "7100"
    if [ "$ENABLE_VPN" = "y" ]; then
      ask PEER_VPN_PORT "对端 VPN UDP 端口" "7200"
      ask PEER_VIP "对端虚拟 IP" "10.0.0.1"
    fi
  fi

  # ---- Dashboard ----
  echo
  ask_yn DASH "启用 Dashboard（Web 管理面板）？" "y"
  if [ "$DASH" = "y" ]; then
    ask DASH_PORT "Dashboard HTTP 端口" "18080"
    ask DASH_USER "Dashboard 用户名" "admin"
    AUTO_PASS="$(gen_token | head -c 16 || true)"
    echo "  Dashboard 密码 (回车=自动生成: $AUTO_PASS)"
    ask DASH_PASS "密码" "$AUTO_PASS"
  fi

  # ---- 拼 yaml ----
  {
    echo "# nlink 配置 —— 由 install.sh 向导生成 $(date '+%F %T')"
    echo ""
    echo "node:"
    echo "  name: \"$NODE_NAME\""
    echo "  token: \"$TOKEN\""
    if [ "$ENABLE_LISTEN" = "y" ]; then
      echo ""
      echo "  listen:"
      echo "    port: $LISTEN_PORT"
      echo "    pool_count: 5"
    fi
    if [ "$DASH" = "y" ]; then
      echo ""
      echo "  dashboard:"
      echo "    enabled: true"
      echo "    port: $DASH_PORT"
      echo "    username: \"$DASH_USER\""
      echo "    password: \"$DASH_PASS\""
    fi
    if [ "$ENABLE_VPN" = "y" ]; then
      echo ""
      echo "  vpn:"
      echo "    enabled: true"
      echo "    virtual_ip: \"$VPN_VIP\""
      echo "    listen_port: $VPN_PORT"
      echo "    mtu: 1400"
    fi
    if [ "$ENABLE_PEER" = "y" ]; then
      echo ""
      echo "peers:"
      echo "  - addr: \"$PEER_ADDR\""
      echo "    port: $PEER_PORT"
      echo "    token: \"$TOKEN\""
      echo "    pool_count: 2"
      if [ "$ENABLE_VPN" = "y" ]; then
        echo "    vpn_port: $PEER_VPN_PORT"
        echo "    virtual_ip: \"$PEER_VIP\""
      fi
    fi
  } > "$CONFIG_FILE"
  chmod 0600 "$CONFIG_FILE"

  info "已写入 $CONFIG_FILE (权限 600)"

  # ---- 启动选项 ----
  echo "" > /dev/tty
  if [ "$SVC_INSTALLED" = "1" ]; then
    ask_yn START_NOW "现在启动 nlink（systemctl enable --now）？" "y"
    if [ "$START_NOW" = "y" ]; then
      systemctl enable nlink >/dev/null 2>&1 || true
      systemctl restart nlink
      sleep 1
      if systemctl is-active --quiet nlink; then
        green "✓ 服务已启动"
        NLINK_STARTED=1
      else
        red "服务启动失败，查看日志: journalctl -u nlink -n 50"
      fi
    fi
  fi

  # 恢复 strict mode
  set -e
  set -o pipefail
fi

# ---- 完成提示 ----
hr
green "✓ 安装完成: $INSTALL_DIR/nlink"
echo
echo "已安装版本:"
"$INSTALL_DIR/nlink" -version 2>/dev/null || true
echo
yellow "运维命令（一个命令搞定所有）:"
echo "  sudo nlink-admin start        启动"
echo "  sudo nlink-admin stop         停止"
echo "  sudo nlink-admin restart      重启"
echo "  sudo nlink-admin reload       热重载配置（SIGHUP，不断连接）"
echo "  sudo nlink-admin status       看状态"
echo "  sudo nlink-admin logs -f      跟日志"
echo "  sudo nlink-admin update       升级到最新版"
echo
if [ "$NLINK_STARTED" = "1" ]; then
  green "✓ 服务已运行"
  if [ "${DASH:-n}" = "y" ]; then
    green "  Dashboard:  http://<本机IP>:${DASH_PORT}  (用户名: $DASH_USER)"
  fi
else
  echo "  当前状态：未启动。运行 ${YELLOW:-}sudo nlink-admin start${NC:-} 即可启动。"
  if [ ! -f "$CONFIG_FILE" ] || grep -q "CHANGE-ME" "$CONFIG_FILE" 2>/dev/null; then
    yellow "  ⚠ 配置 $CONFIG_FILE 含占位符，先 sudo vi 改一下"
  fi
fi
echo
echo "  重新跑配置向导:      sudo rm $CONFIG_FILE && curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.sh | sudo bash"
hr
