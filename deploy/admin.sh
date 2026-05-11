#!/usr/bin/env bash
# nlink 运维入口 —— 把日常操作（启停 / 重启 / 升级 / 看日志）聚到一个命令下
#
# 用法:
#   sudo /opt/nlink/admin.sh <command> [args]
#
# 命令:
#   start              启动服务
#   stop               停止服务
#   restart            重启服务
#   reload             SIGHUP 重载配置 (不中断连接)
#   status             看运行状态
#   logs [-f]          看日志, -f 持续跟随
#   update [version]   升级到最新或指定版本
#   version            打印 nlink 版本
#   config             打印当前配置文件路径

set -euo pipefail

# 允许通过 INSTALL_DIR 覆盖
INSTALL_DIR="${INSTALL_DIR:-$(dirname "$(readlink -f "$0" 2>/dev/null || echo "$0")")}"
[ -d "$INSTALL_DIR" ] || INSTALL_DIR="/opt/nlink"
BIN="$INSTALL_DIR/nlink"
CONFIG="$INSTALL_DIR/config/nlink.yaml"
LOG="$INSTALL_DIR/nlink.log"
SVC="nlink"

red()    { printf '\033[31m%s\033[0m\n' "$*" >&2; }
green()  { printf '\033[32m%s\033[0m\n' "$*"; }
yellow() { printf '\033[33m%s\033[0m\n' "$*"; }
info()   { printf '→ %s\n' "$*"; }

# 检测运行模式: systemd / manual
detect_mode() {
  if command -v systemctl >/dev/null 2>&1 \
     && systemctl list-unit-files --no-pager 2>/dev/null | grep -q "^${SVC}\.service"; then
    echo systemd
  else
    echo manual
  fi
}

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    red "请用 root / sudo 执行此命令"
    exit 1
  fi
}

ensure_bin() {
  if [ ! -x "$BIN" ]; then
    red "找不到二进制: $BIN"
    red "先跑安装: curl -fsSL https://raw.githubusercontent.com/hsqbyte/nlink/master/deploy/install.sh | sudo bash"
    exit 1
  fi
}

cmd_start() {
  require_root
  ensure_bin
  case "$(detect_mode)" in
    systemd)
      systemctl start "$SVC"
      sleep 1
      systemctl is-active --quiet "$SVC" && green "✓ 已启动" || { red "启动失败"; systemctl status "$SVC" --no-pager; exit 1; }
      ;;
    manual)
      if pgrep -f "$BIN" >/dev/null 2>&1; then
        yellow "已在运行 (PID: $(pgrep -f "$BIN" | xargs))"
        return
      fi
      [ -f "$CONFIG" ] || { red "缺少配置: $CONFIG"; exit 1; }
      cd "$INSTALL_DIR"
      nohup "$BIN" -c "$CONFIG" >> "$LOG" 2>&1 &
      sleep 1
      if pgrep -f "$BIN" >/dev/null 2>&1; then
        green "✓ 已启动 (PID: $(pgrep -f "$BIN" | xargs)), 日志: $LOG"
      else
        red "启动失败, 看日志:"
        tail -n 30 "$LOG" 2>/dev/null || true
        exit 1
      fi
      ;;
  esac
}

cmd_stop() {
  require_root
  case "$(detect_mode)" in
    systemd)
      systemctl stop "$SVC"
      green "✓ 已停止"
      ;;
    manual)
      if ! pgrep -f "$BIN" >/dev/null 2>&1; then
        yellow "进程未运行"
        return
      fi
      pkill -TERM -f "$BIN" || true
      for i in 1 2 3 4 5 6; do
        pgrep -f "$BIN" >/dev/null 2>&1 || break
        sleep 0.5
      done
      if pgrep -f "$BIN" >/dev/null 2>&1; then
        yellow "进程没在 3 秒内优雅退出，强制 KILL"
        pkill -KILL -f "$BIN" || true
      fi
      green "✓ 已停止"
      ;;
  esac
}

cmd_restart() {
  require_root
  ensure_bin
  case "$(detect_mode)" in
    systemd)
      systemctl restart "$SVC"
      sleep 1
      systemctl is-active --quiet "$SVC" && green "✓ 已重启" || { red "重启失败"; systemctl status "$SVC" --no-pager; exit 1; }
      ;;
    manual)
      cmd_stop
      cmd_start
      ;;
  esac
}

cmd_reload() {
  require_root
  ensure_bin
  case "$(detect_mode)" in
    systemd)
      systemctl reload "$SVC" 2>/dev/null \
        || systemctl kill -s HUP "$SVC"
      green "✓ 已发送 SIGHUP"
      ;;
    manual)
      pgrep -f "$BIN" >/dev/null 2>&1 || { red "进程未运行"; exit 1; }
      pkill -HUP -f "$BIN"
      green "✓ 已发送 SIGHUP"
      ;;
  esac
}

cmd_status() {
  case "$(detect_mode)" in
    systemd)
      systemctl status "$SVC" --no-pager || true
      ;;
    manual)
      if pgrep -fa "$BIN" >/dev/null 2>&1; then
        green "● nlink 运行中"
        pgrep -fa "$BIN"
      else
        red "○ nlink 未运行"
        exit 3
      fi
      ;;
  esac
}

cmd_logs() {
  local follow=""
  [ "${1:-}" = "-f" ] && follow="-f"
  case "$(detect_mode)" in
    systemd)
      if [ -n "$follow" ]; then
        exec journalctl -u "$SVC" -f --no-pager
      else
        exec journalctl -u "$SVC" -n 100 --no-pager
      fi
      ;;
    manual)
      [ -f "$LOG" ] || { yellow "日志文件不存在: $LOG"; exit 0; }
      if [ -n "$follow" ]; then
        exec tail -f "$LOG"
      else
        tail -n 100 "$LOG"
      fi
      ;;
  esac
}

cmd_update() {
  require_root
  if [ ! -x "$INSTALL_DIR/update.sh" ]; then
    red "找不到 $INSTALL_DIR/update.sh"
    exit 1
  fi
  exec "$INSTALL_DIR/update.sh" "$@"
}

cmd_version() {
  ensure_bin
  "$BIN" -version 2>/dev/null || "$BIN" --version 2>/dev/null || echo "未知"
}

cmd_config() {
  echo "$CONFIG"
  [ -f "$CONFIG" ] && green "  存在" || red "  不存在"
}

usage() {
  cat <<EOF
用法: $(basename "$0") <command> [args]

服务管理:
  start          启动 nlink
  stop           停止 nlink
  restart        重启 nlink
  reload         SIGHUP 重载配置（不中断连接）
  status         查看状态

观察:
  logs           看最近 100 行日志
  logs -f        持续跟随日志

维护:
  update         升级到最新版本
  update <ver>   升级到指定版本（如 update v2.9.5）

信息:
  version        打印 nlink 版本
  config         打印配置路径

安装目录: $INSTALL_DIR
EOF
}

case "${1:-}" in
  start)        shift; cmd_start "$@" ;;
  stop)         shift; cmd_stop "$@" ;;
  restart)      shift; cmd_restart "$@" ;;
  reload)       shift; cmd_reload "$@" ;;
  status)       shift; cmd_status "$@" ;;
  logs|log)     shift; cmd_logs "$@" ;;
  update|upgrade) shift; cmd_update "$@" ;;
  version|-v|--version) cmd_version ;;
  config)       cmd_config ;;
  ""|help|-h|--help) usage ;;
  *)            red "未知命令: $1"; usage; exit 1 ;;
esac
