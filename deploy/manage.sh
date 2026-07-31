#!/usr/bin/env bash
#
# traffic-keeper 一键管理脚本：安装 / 更新 / 卸载 master & agent。
#
# 用法（交互式数字菜单）:
#   curl -fsSL https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/manage.sh | bash
#   或下载后: sudo ./deploy/manage.sh
#
# 非交互（直接执行某项）:
#   sudo ./manage.sh install-master
#   sudo ./manage.sh update-agent
#   sudo ./manage.sh uninstall-agent
#   支持: install-master|install-agent|update-master|update-agent|uninstall-master|uninstall-agent
#
# 注意: 所有交互 read 都显式 </dev/tty —— 这样 `curl|bash`（bash 从管道读脚本）
# 时，菜单输入仍从终端读，不会被管道里的脚本内容"喂"进去。
#
set -o pipefail

REPO="PiPi-happy/traffic-keeper"
INSTALL_DIR="/usr/local/bin"
STATE_DIR="/var/lib/traffic-keeper"
GHPROXY="${GHPROXY:-https://gh-proxy.org}"   # 留空则直连 GitHub

MASTER_BIN="${INSTALL_DIR}/tk-master"
MASTER_SVC="traffic-keeper-master"
MASTER_UNIT="/etc/systemd/system/${MASTER_SVC}.service"
MASTER_DB="${STATE_DIR}/traffic-keeper.db"

AGENT_BIN="${INSTALL_DIR}/traffic-keeper-agent"
AGENT_SVC="traffic-keeper-agent"
AGENT_UNIT="/etc/systemd/system/${AGENT_SVC}.service"
AGENT_STATE="${STATE_DIR}/agent.state.json"

# ---------- helpers ----------
arch() {
	case "$(uname -m)" in
		x86_64 | amd64) echo amd64 ;;
		aarch64 | arm64) echo arm64 ;;
		*) echo "error: 不支持的架构 $(uname -m)" >&2; exit 1 ;;
	esac
}

ghurl() {
	local u="https://github.com/${REPO}/releases/latest/download/$1"
	[ -n "$GHPROXY" ] && u="${GHPROXY}/${u}"
	echo "$u"
}

dl() {
	local tmp; tmp="$(mktemp)"
	if ! curl -fsSL -o "$tmp" "$1"; then
		echo "error: 下载失败: $1" >&2
		echo "  tip: 换镜像 GHPROXY=https://ghproxy.com 或 GHPROXY= （直连）" >&2
		rm -f "$tmp"; return 1
	fi
	install -m 0755 "$tmp" "$2"; rm -f "$tmp"
}

svc_active() { systemctl is-active --quiet "$1" 2>/dev/null; }
state_of() { svc_active "$1" && echo "运行中" || echo "未运行"; }

preflight() {
	[ "$(id -u)" -eq 0 ] || { echo "error: 请用 root 运行（sudo）" >&2; exit 1; }
	[ "$(uname -s)" = Linux ] || { echo "error: 仅支持 Linux" >&2; exit 1; }
	command -v systemctl >/dev/null 2>&1 || { echo "error: 未找到 systemd" >&2; exit 1; }
	command -v curl >/dev/null 2>&1 || { echo "error: 未找到 curl" >&2; exit 1; }
}

# ---------- master ----------
install_master() {
	echo "=== 安装 Master ==="
	if [ -x "$MASTER_BIN" ]; then echo "master 已安装（用「更新 Master」升级）"; return 0; fi
	local default_url="http://$(hostname -I 2>/dev/null | awk '{print $1}'):8080"
	local base pw
	read -rp "MASTER_BASE_URL（agent 连接用，默认 $default_url）: " base </dev/tty
	base="${base:-$default_url}"
	read -rsp "MASTER_ADMIN_PASSWORD（面板登录密码，≥6位）: " pw </dev/tty; echo
	[ "${#pw}" -ge 6 ] || { echo "error: 密码至少 6 位" >&2; return 1; }

	mkdir -p "$STATE_DIR"
	echo "下载 master (linux/$(arch))..."
	dl "$(ghurl "traffic-keeper-master-linux-$(arch)")" "$MASTER_BIN" || return 1

	cat >"$MASTER_UNIT" <<EOF
[Unit]
Description=Traffic Keeper Master
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${MASTER_BIN}
Environment=MASTER_ADDR=:8080
Environment=MASTER_DB=${MASTER_DB}
Environment=MASTER_BASE_URL=${base}
Environment=MASTER_ADMIN_PASSWORD=${pw}
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload
	systemctl enable "$MASTER_SVC" >/dev/null 2>&1 || true
	systemctl restart "$MASTER_SVC"
	echo "✓ master 已安装并启动 → http://$(hostname -I 2>/dev/null | awk '{print $1}'):8080"
	echo "  日志: journalctl -u $MASTER_SVC -f"
}

update_master() {
	[ -x "$MASTER_BIN" ] || { echo "master 未安装（$MASTER_BIN 不存在）" >&2; return 1; }
	echo "更新 master（拉取最新 release，保留数据库与配置）..."
	dl "$(ghurl "traffic-keeper-master-linux-$(arch)")" "$MASTER_BIN" || return 1
	systemctl restart "$MASTER_SVC"
	echo "✓ master 已更新并重启"
}

uninstall_master() {
	[ -e "$MASTER_UNIT" ] || [ -x "$MASTER_BIN" ] || { echo "master 未安装" >&2; return 1; }
	echo "卸载 master（数据库 $MASTER_DB 保留，不自动删）"
	local c; read -rp "确认卸载 master？[y/N] " c </dev/tty
	[[ "$c" =~ ^[Yy] ]] || { echo "已取消"; return 0; }
	systemctl stop "$MASTER_SVC" 2>/dev/null || true
	systemctl disable "$MASTER_SVC" 2>/dev/null || true
	rm -f "$MASTER_BIN" "$MASTER_UNIT"
	systemctl daemon-reload
	echo "✓ master 已卸载（数据库保留在 $MASTER_DB；如需彻底删除请手动 rm）"
}

# ---------- agent ----------
install_agent() {
	echo "=== 安装 Agent ==="
	local token server
	read -rp "token（面板「新建节点」生成的安装 token）: " token </dev/tty
	[ -n "$token" ] || { echo "error: token 必填" >&2; return 1; }
	read -rp "master 地址（如 https://master.example.com 或 http://1.2.3.4:8080）: " server </dev/tty
	[ -n "$server" ] || { echo "error: server 必填" >&2; return 1; }

	mkdir -p "$STATE_DIR"
	echo "下载 agent (linux/$(arch))..."
	dl "$(ghurl "traffic-keeper-agent-linux-$(arch)")" "$AGENT_BIN" || return 1

	cat >"$AGENT_UNIT" <<EOF
[Unit]
Description=Traffic Keeper Agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=${AGENT_BIN} run --state ${AGENT_STATE}
ExecReload=/bin/kill -HUP \$MAINPID
Restart=always
RestartSec=10
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths=${STATE_DIR} ${INSTALL_DIR}
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
EOF
	systemctl daemon-reload
	systemctl enable "$AGENT_SVC" >/dev/null 2>&1 || true
	systemctl stop "$AGENT_SVC" >/dev/null 2>/dev/null || true
	"${AGENT_BIN}" add --server "${server}" --token "${token}" --state "${AGENT_STATE}"
	systemctl start "$AGENT_SVC"
	echo "✓ agent 已安装并启动"
	echo "  日志: journalctl -u $AGENT_SVC -f"
}

update_agent() {
	[ -x "$AGENT_BIN" ] || { echo "agent 未安装（$AGENT_BIN 不存在）" >&2; return 1; }
	echo "更新 agent（拉取最新 release，保留已配置的 master 凭据）..."
	dl "$(ghurl "traffic-keeper-agent-linux-$(arch)")" "$AGENT_BIN" || return 1
	systemctl restart "$AGENT_SVC"
	echo "✓ agent 已更新并重启（state 保留）"
}

uninstall_agent() {
	[ -e "$AGENT_UNIT" ] || [ -x "$AGENT_BIN" ] || { echo "agent 未安装" >&2; return 1; }
	echo "卸载 agent"
	local c; read -rp "确认卸载 agent？[y/N] " c </dev/tty
	[[ "$c" =~ ^[Yy] ]] || { echo "已取消"; return 0; }
	systemctl stop "$AGENT_SVC" 2>/dev/null || true
	systemctl disable "$AGENT_SVC" 2>/dev/null || true
	rm -f "$AGENT_BIN" "$AGENT_UNIT"
	systemctl daemon-reload
	local cc; read -rp "同时删除 agent state（已配置的 master 凭据）？[y/N] " cc </dev/tty
	if [[ "$cc" =~ ^[Yy] ]]; then rm -f "$AGENT_STATE"; echo "  state 已删"; fi
	echo "✓ agent 已卸载"
}

# ---------- menu ----------
show_menu() {
	echo
	echo "========== Traffic Keeper 管理 =========="
	if [ -x "$MASTER_BIN" ]; then echo "  master: 已安装（$(state_of "$MASTER_SVC")）"; else echo "  master: 未安装"; fi
	if [ -x "$AGENT_BIN" ]; then echo "  agent : 已安装（$(state_of "$AGENT_SVC")）"; else echo "  agent : 未安装"; fi
	echo "----------------------------------------"
	echo "  1) 安装 Master"
	echo "  2) 安装 Agent"
	echo "  3) 更新 Master"
	echo "  4) 更新 Agent"
	echo "  5) 卸载 Master"
	echo "  6) 卸载 Agent"
	echo "  0) 退出"
	echo "========================================"
}

menu() {
	while true; do
		show_menu
		local n; read -rp "请选择 [0-6]: " n </dev/tty
		case "$n" in
			1) install_master ;;
			2) install_agent ;;
			3) update_master ;;
			4) update_agent ;;
			5) uninstall_master ;;
			6) uninstall_agent ;;
			0) echo "再见"; exit 0 ;;
			*) echo "无效选择：$n" ;;
		esac
		echo
		read -rp "按回车返回菜单..." _ </dev/tty
	done
}

preflight
case "${1:-}" in
	install-master) install_master ;;
	install-agent) install_agent ;;
	update-master) update_master ;;
	update-agent) update_agent ;;
	uninstall-master) uninstall_master ;;
	uninstall-agent) uninstall_agent ;;
	"") menu ;;
	*) echo "未知命令: $1" >&2; echo "可用: install-master|install-agent|update-master|update-agent|uninstall-master|uninstall-agent" >&2; exit 1 ;;
esac
