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

## Cloudflare Tunnel（可选，国际链路 / 未备案优化）

agent 在境外、master 在国内时，明文大上传容易被 GFW/链路 RST；master 未备案也无法用域名 443 HTTPS。面板顶栏「Cloudflare Tunnel」可一键开启：

- master 自动安装 cloudflared（经 gh-proxy.org）并建立 quick tunnel，分配 `https://xxx.trycloudflare.com`。
- agent 的**上传数据自动改走 tunnel**（加密、避开 RST，通常比直连快），**控制面（心跳/策略）仍走直连**——tunnel 即使抖动，agent 也不离线。
- **无需域名、无需备案**：tunnel 是 master 主动出站建立，不开任何入站端口。

面板点「Cloudflare Tunnel」→ 启用，看实时安装日志即可。size 建议 ≤ 5MB（国际链路带宽有限，单次约 100s 内传完最稳；想加流量优先调小 interval）。master 重启后 tunnel 会按上次意图**自动重开**（新 trycloudflare URL，agent ≤30s 跟随）。

## GitHub 加速源（面板可配，地区感知）

agent 自升级 / 首次安装要从 GitHub Releases 下载二进制，国内直连不通。面板顶栏「🌐 加速源」可配置镜像地址（如 `https://gh-proxy.org`）：

- 仅对上报地区为 **CN** 的 agent 生效（走加速）；海外 agent 始终直连 GitHub。
- agent 启动通过 ip-api.com 探测国家码并上报 master，master 据此决定下发直连还是加速 URL。
- 留空则中国 agent 也直连（可能下载失败）。

## 多 Master（一发多收）

一个 agent 可以同时向**多个 master** 上传。跑某个 master 的安装命令 = 在 agent 上「添加一个 master」；同 master 重复跑会**覆盖凭证**，不会重复添加。在 agent 机器用 CLI 管理：

```bash
traffic-keeper-agent list                              # 查看已配置的 master
traffic-keeper-agent add --server <URL> --token <T>     # 添加/覆盖一个 master
traffic-keeper-agent stop <server>                      # 暂停向某 master 上传（保留配置）
traffic-keeper-agent start <server>                     # 恢复
traffic-keeper-agent remove <server>                    # 删除一个 master
```

- 同一台 VPS 跑多个 master 的安装命令 = 添加多个 master，agent 同时向所有未 stop 的 master 上传（add/remove/stop/start 后自动热加载，无需重启服务）。
- 上传日志标明目标地址（`uploaded N to https://xxx.trycloudflare.com/upload/<id>` 或 IP），一眼看出是否走了 tunnel。
- 每个 master 在服务端是一个独立 agent（各自统计/策略/升级），互不影响。

## 一键管理脚本

`deploy/manage.sh` 提供交互式数字菜单，集成 master / agent 的安装、更新、卸载：

```bash
curl -fsSL https://raw.githubusercontent.com/PiPi-happy/traffic-keeper/main/deploy/manage.sh | bash
# 或下载后: sudo ./deploy/manage.sh
```

菜单含：安装/更新/卸载 Master、安装/更新/卸载 Agent。自动检测本机已装组件并显示运行状态；国内默认走 gh-proxy 下载，`GHPROXY=` 可换镜像或留空直连。也支持非交互：`sudo ./manage.sh update-master`、`sudo ./manage.sh uninstall-agent` 等。

## 开发

```bash
make test           # 跑全部测试
make web            # 构建前端并 stage 到 embed 目录
make build-master   # 构建含前端的 master 二进制（含 make web）
make build-agent    # 构建 agent 二进制
go run ./cmd/agent  # 本地跑 agent
```

## 功能与路线

**已完成（v0.9.2）**：
- Agent 注册 / 心跳 / 上传（心跳、拉策略、上传三个独立 goroutine，上传再慢也不会被判离线）
- **Cloudflare Tunnel 默认 http2（v0.9.2）**：线上实测 GFW 现严重干扰 UDP 7874(quic)，http2(TCP 443) 快 ~20x——quic 5MB/95s 超时 vs http2 5MB/4.6s（agent 实战 620KB/s）；附优选 IP 探针（仅 quic 模式辅助，http2 不需要）
- 面板（acme 设计系统 / Inter / lucide 图标）：节点表（在线/地区/版本/累计上行/策略）、策略编辑（固定/随机 **流量大小与上传间隔** segmented）、一键生成安装命令、密码修改、24h 上传曲线 + 详情
- **仪表盘页**：左侧导航新增仪表盘（在线/累计上行/上传次数/平均速率 KPI、24h 全台上行趋势曲线、上传成功率、各地区节点分布）；节点列表加宽占满、移除自动刷新改为手动、新建/刷新按钮上移
- **随机上传间隔**：策略支持固定/随机间隔（每次上传后在 min~max 秒间随机等待），与流量大小随机同理
- **EOF 自动恢复**：master 升级/重启后 tunnel 换新 URL，agent 上传失败自动刷新策略、tunnel 未就绪时退避，不再持续刷 EOF、无需手动重启 agent
- **Agent 多 Master（一发多收）**：一个 agent 同时向多个 master 上传；CLI `add/list/remove/stop/start` 管理.master 列表；同 master 覆盖凭证、热加载；上传日志带目标地址
- **Agent 自升级**：面板点升级图标，agent 自动下载替换重启（按地区路由下载源）
- **Cloudflare Tunnel**（可选：加密 agent 上传，绕开未备案 443 与 GFW RST；master 重启自动恢复）
- **GitHub 加速源可配 + 地区感知**：面板配置加速源，中国 agent 走加速、海外直连
- 一行安装（install.sh）、Caddy HTTPS 部署、agent 版本/地区上报

**V1 / V2 计划**：WebSocket 实时指令、分组批量、代理链（WARP / SOCKS5）、流量伪装、多目标上传、控制面来源白名单与 named tunnel。

## 状态

✅ 稳定运行中 —— 最新 [v0.9.2](https://github.com/PiPi-happy/traffic-keeper/releases/latest)。

## License

MIT
