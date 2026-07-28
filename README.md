# Traffic Keeper

> 自用的 VPS 上行流量保活管控平台 —— 一个面板端（控制面 + 数据面接收端）统一纳管所有发送端 Agent。

![status](https://img.shields.io/badge/status-MVP-green) ![license](https://img.shields.io/badge/license-MIT-blue) ![release](https://img.shields.io/github/v/release/PiPi-happy/traffic-keeper)

## 为什么做这个

让被纳管的 VPS 持续产生 **上行流量**、下行接近为零（保活场景）。面板端集中调度所有发送端：定时上传、固定/随机包大小、一键启停。

## 架构

```
              ┌─────────────────────────────────────────────┐
              │     Master 面板端（控制面 + 数据面接收端）      │
              │     Web UI  ↔  策略引擎  ↔  HTTP 接收服务      │
              └──────────┬─────────────────────┬────────────┘
                         │ 控制面(小流量)        │ 上行(大流量)
          ┌──────────────┘                     │
     ┌─────┴────┐   ┌────────┐   ┌─────────────┴──┐
     │ Agent A  │   │ Agent B│   │   Agent N      │   各台被管 VPS
     └──────────┘   └────────┘   └────────────────┘
```

- **控制面**：Agent 主动出站连 Master —— 心跳、拉取策略、接收启停（流量极小，且天然适配出站受限的 VPS）。
- **数据面**：Agent 按策略生成随机（不可压缩）数据 PUT 到 Master；Master 接收即丢弃 + 计数，**只回 2 字节**，下行接近 0。

## 快速开始

### 1. 部署 Master（面板端 + 接收端）

**方式 A — Docker Compose（推荐，自带 HTTPS）**

需要一台公网 IP 的服务器 + 一个 A 记录指向它的域名。

```bash
git clone https://github.com/PiPi-happy/traffic-keeper.git
cd traffic-keeper/deploy
cp .env.example .env   # 编辑：MASTER_DOMAIN / MASTER_BASE_URL / MASTER_ADMIN_PASSWORD
docker compose up -d --build
```

浏览器打开 `https://<你的域名>`，用 `.env` 里的密码登录。详见 [`deploy/README.md`](deploy/README.md)。

**方式 B — 二进制（快速测试，HTTP）**

```bash
curl -fsSL -o tk-master \
  https://github.com/PiPi-happy/traffic-keeper/releases/latest/download/traffic-keeper-master-linux-amd64
# arm64 机器把 amd64 换成 arm64
chmod +x tk-master
MASTER_DB=/var/lib/tk.db MASTER_ADDR=:8080 \
MASTER_BASE_URL=http://<本机公网IP>:8080 \
MASTER_ADMIN_PASSWORD=你的密码 \
./tk-master
```

> 测试用 HTTP 即可；正式环境请用方式 A（Caddy 自动 HTTPS，加密 token 与上传数据）。

### 2. 安装 Agent（发送端 VPS）

在 Master 面板点「新建节点」，复制生成的命令，到目标 VPS 上以 root 粘贴执行：

```bash
curl -fsSL https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/install.sh \
  | bash -s -- --token <面板生成的TOKEN> --server https://<你的域名>
```

脚本自动下载 agent、安装 systemd 服务并启动。Agent 注册后按策略周期性上传。查看日志：

```bash
journalctl -u traffic-keeper-agent -f
```

面板上节点变**在线**、**累计上行**持续增长，即表示链路打通。

## 配置（Master 环境变量）

| 变量 | 说明 | 默认 |
|------|------|------|
| `MASTER_ADDR` | 监听地址 | `:8080` |
| `MASTER_DB` | SQLite 路径 | `traffic-keeper.db` |
| `MASTER_BASE_URL` | 公网地址（用于生成 agent 安装命令里的 `--server`） | — |
| `MASTER_ADMIN_PASSWORD` | 面板登录密码 | — |

## 开发

```bash
make test           # 跑全部测试
make web            # 构建前端并 stage 到 embed 目录
make build-master   # 构建含前端的 master 二进制（含 make web）
make build-agent    # 构建 agent 二进制
go run ./cmd/agent  # 本地跑 agent
```

## 功能与路线

**MVP（已完成）**：Agent 注册 / 心跳 / 上传 · 面板（节点列表、累计上行、策略编辑、一键生成安装命令）· 一行安装 · Caddy HTTPS。

**V1 / V2 计划**：随机化（时间抖动 / 包大小 `[min,max]` 范围）、WebSocket 实时指令、分组批量、代理链（WARP / SOCKS5）、流量伪装、多目标上传。

## 状态

✅ MVP 已完成并发版 —— 最新 [v0.1.1](https://github.com/PiPi-happy/traffic-keeper/releases/latest)。

## License

MIT
