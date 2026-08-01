# Traffic Keeper

> 自用的 VPS 上行流量保活平台 —— 一个 Master（面板 + 接收端）统一纳管所有 Agent（发送端，跑在被保活 VPS 上）。

![status](https://img.shields.io/badge/status-MVP-green) ![license](https://img.shields.io/badge/license-MIT-blue) ![release](https://img.shields.io/github/v/release/PiPi-happy/traffic-keeper)

---

## 一、项目简介

让被纳管的 VPS 持续产生**上行流量**、下行接近为零（保活场景）。Master 集中调度所有 Agent：定时上传、固定/随机包大小与间隔、一键启停、自升级。

```
              ┌─────────────────────────────────────────────┐
              │     Master（控制面 + 数据面接收端）             │
              │     Web UI  ↔  策略引擎  ↔  HTTP 接收服务      │
              └──────────┬─────────────────────┬────────────┘
                         │ 控制面(小流量)        │ 上行(大流量)
          ┌──────────────┘                     │
     ┌─────┴────┐   ┌────────┐   ┌─────────────┴──┐
     │ Agent A  │   │ Agent B│   │   Agent N      │   各台被管 VPS
     └──────────┘   └────────┘   └────────────────┘
```

- **控制面**：Agent 主动出站连 Master（心跳/拉策略/启停），流量极小，天然适配出站受限的 VPS。
- **数据面**：Agent 按策略 PUT 随机不可压缩数据到 Master；Master 接收即丢弃 + 计数，**只回 2 字节**，下行≈0。

**技术栈**：Go 单二进制（master + agent，`modernc.org/sqlite` 纯 Go 无 CGO）· SQLite · Vue3 + Element Plus + ECharts · Caddy（部署）· GitHub Actions（多架构发版）。

**主要特性**：仪表盘（KPI / 24h 趋势 / 成功率 / 地区分布）+ 节点管理 · 策略固定/随机（流量大小 + 上传间隔）· **Cloudflare Tunnel 默认 http2**（v0.9.2，绕 GFW 对 UDP 7874 的干扰，实测 ~20×）· 优选 IP 探针 · Agent 多 Master（一发多收）· Agent 自升级 · master 重启后 agent 自动恢复（EOF/5xx 退避 + 刷新策略）。

---

## 二、安装方法

### 1. 推荐：一键管理脚本

`deploy/manage.sh` 交互式数字菜单，集成 **master / agent 的安装 / 更新 / 卸载**（自动检测已装组件、显示运行状态；国内默认走 gh-proxy）：

```bash
curl -fsSL https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/manage.sh | bash
# 或下载后: sudo ./deploy/manage.sh
# 非交互:   sudo ./manage.sh install-master | update-agent | uninstall-agent ...
```

### 2. 安装 Agent（一行命令，面板生成）

在 Master 面板点「新建节点」复制命令，到目标 VPS 以 root 执行：

```bash
curl -fsSL https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/install.sh \
  | bash -s -- --token <面板生成的TOKEN> --server https://<你的域名或IP:端口>
```

查看日志：`journalctl -u traffic-keeper-agent -f`。面板上节点变**在线**、**累计上行**增长即链路打通。

### 3. 其它部署方式

**Docker Compose（Master，自带 HTTPS）** —— 需公网 IP + 域名 A 记录：

```bash
git clone https://github.com/PiPi-happy/traffic-keeper.git && cd traffic-keeper/deploy
cp .env.example .env   # 编辑 MASTER_DOMAIN / MASTER_BASE_URL / MASTER_ADMIN_PASSWORD
docker compose up -d --build
```

**二进制（快速测试，HTTP）**：

```bash
curl -fsSL -o tk-master \
  https://github.com/PiPi-happy/traffic-keeper/releases/latest/download/traffic-keeper-master-linux-amd64
chmod +x tk-master
MASTER_BASE_URL=http://<本机IP>:8080 MASTER_ADMIN_PASSWORD=你的密码 ./tk-master
```

> 测试可用 HTTP；正式建议 Docker（Caddy 自动 HTTPS，加密 token 与上传数据）。

### 4. 配置（Master 环境变量）

| 变量 | 说明 | 默认 |
|------|------|------|
| `MASTER_ADDR` | 监听地址 | `:8080` |
| `MASTER_DB` | SQLite 路径 | `traffic-keeper.db` |
| `MASTER_BASE_URL` | 公网地址（生成 agent 安装命令里的 `--server`） | — |
| `MASTER_ADMIN_PASSWORD` | 面板登录密码（首次 seed） | — |

### 5. 运行说明

- **Cloudflare Tunnel**（面板顶栏开关）：master 装 cloudflared 建 quick tunnel，agent 上传自动走 tunnel（加密、绕 GFW RST / 未备案 443），控制面仍直连。**默认 http2**（v0.9.2，实测远快于 quic）；master 重启自动恢复，agent 自动跟随。
- **GitHub 加速源**（面板「🌐 加速源」）：agent 自升级/安装下载 GitHub Releases，国内直连不通；配镜像（如 `https://gh-proxy.org`）后 **CN 的 agent** 走加速、海外直连。
- **多 Master（一发多收）**：一个 agent 可同时向多个 master 上传，在 agent 机用 CLI 管理（热加载，无需重启）：
  ```bash
  traffic-keeper-agent list | add --server <URL> --token <T> | stop <server> | start <server> | remove <server>
  ```

---

## 三、开发方法

```bash
export PATH="$HOME/.local/go/bin:$PATH"          # 本机 go 装在这（brew 国内卡改阿里云镜像）
export GOPROXY=https://goproxy.cn,direct
export npm_config_registry=https://registry.npmmirror.com

make test            # 全测试
make web             # 前端 build + stage 到 internal/web/dist（改前端后必跑，否则 master 服务旧 UI）
make build-master    # 含前端的 master 二进制（ldflags version=git describe）
make build-agent     # agent 二进制
go run ./cmd/agent   # 本地跑 agent
```

**发版**：改代码 → `commit` → `git tag v<x>.<y>.<zzz>`（patch 用 3 位，如 `v0.10.001`）→ `push --tags`，GitHub Actions 多架构发版。master/agent 部署走 release（国内经 gh-proxy）；agent 也可面板点「升级」自升级。

**测试要点**：agent 多 master 用 `startFakeMaster`（真 store+master+httptest），interval 改 var 调小；store 用 `t.TempDir()` 真实 SQLite。

---

✅ 稳定运行中 —— 最新 [v0.9.2](https://github.com/PiPi-happy/traffic-keeper/releases/latest)。MIT License.
