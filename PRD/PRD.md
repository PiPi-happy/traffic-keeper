# Traffic Keeper — PRD（产品需求文档）

> **给 AI / 新接手者的快速入口**：这是一个 Go + Vue3 的 VPS 上行流量保活平台。一个 Master（面板端=接收端）纳管多个 Agent（发送端，跑在被保活 VPS 上），Agent 主动出站连 Master、按策略生成随机数据 PUT 上传，Master 接收即丢弃+计数（下行≈0）。一个 Agent 还能同时连多个 Master（一发多收）。读本文档 + 仓库代码即可接续开发。**最新版本 v0.9.004**（http2 默认 + tunnel 自愈 + 上传停滞哨兵；线上 agent 均 v0.9.003）。

---

## 1. 项目定位

让被纳管的 VPS 持续产生**上行流量**、下行接近为零（保活场景）。面板端集中调度所有发送端：定时上传、固定/随机包大小、一键启停、自升级、CF Tunnel 加密。

- **仓库**：`git@github.com:PiPi-happy/traffic-keeper.git`（公开，SSH 推送）
- **技术栈**：Go（master+agent 单二进制，`modernc.org/sqlite` 纯 Go 无 CGO）· SQLite · Vue3 + Element Plus + ECharts + lucide · Inter(@fontsource 离线) · Caddy(部署) · GitHub Actions(多架构发版)
- **设计原则**：扁平(acme 风格)、轻量(<20 台自用)、单二进制、agent 主动出站(适配出站受限 VPS)

## 2. 架构

```
              ┌─────────────────────────────────────────────┐
              │     Master 面板端（控制面 + 数据面接收端）      │
              │     Web UI  ↔  策略引擎  ↔  HTTP 接收服务      │
              │   (可选 cloudflared → trycloudflare tunnel)   │
              └──────────┬─────────────────────┬────────────┘
                         │ 控制面(小流量)        │ 上行(大流量, 可走 tunnel)
          ┌──────────────┘                     │
     ┌─────┴────┐   ┌────────┐   ┌─────────────┴──┐
     │ Agent A  │   │ Agent B│   │   Agent N      │   各台被管 VPS
     │ (1→多master)│ └────────┘   └────────────────┘
     └──────────┘
```

- **控制面**：Agent 主动出站连 Master（register/heartbeat/policy），Bearer secret 鉴权，流量极小。
- **数据面**：Agent PUT 随机数据到 Master（`io.Discard` 流式计数，响应仅 "ok" 2 字节，下行≈0）。数据面可走 tunnel（`policy.upload_url`），控制面始终直连。
- **多 master**：一个 Agent 持 N 套凭证，向 N 个 Master 同时上传（master 侧零改动，每个 master 是独立 agent）。

## 3. 目录结构

```
traffic-keeper/
├── cmd/
│   ├── master/main.go        # 面板端入口（systemd/docker）
│   └── agent/main.go         # 发送端入口（CLI 子命令：run/add/list/remove/stop/start + legacy）
├── internal/
│   ├── master/               # master 逻辑
│   │   ├── server.go         # Server + routes + 全局 helpers(writeJSON/clientIP/isOnline) + cleaner
│   │   ├── auth.go           # bcrypt 密码 + sessionStore + requireAdmin + authenticateAgent + 常量
│   │   ├── agent_api.go      # agent-facing: register/heartbeat(版本+升级指令)/policy
│   │   ├── panel_api.go      # panel-facing: login/password/nodes CRUD/events/install-cmd/upgrade/gh-proxy
│   │   ├── upload.go         # 数据面 PUT /upload/{id}（io.Discard 计数 + duration + recordEvent）
│   │   ├── tunnel.go         # TunnelManager（cloudflared 装/启停/日志 + handlers + RestoreTunnelIntent + buildDownloadURL）
│   │   └── store/            # SQLite（agents/policies/stats/settings/upload_events + migrate）
│   ├── agent/                # agent 逻辑
│   │   └── agent.go          # masterConn + Agent supervisor + state v2/迁移 + reconcile(SIGHUP) + CLI helpers + selfUpgrade
│   └── web/                  # go:embed 前端 dist
├── web/                      # Vue3 前端源码（vite build → internal/web/dist embed）
├── deploy/                   # install.sh(agent) + Dockerfile + docker-compose + Caddyfile + .env.example
├── .github/workflows/release.yml  # 多架构发版（ldflags 注入 version）
└── Makefile                  # web/build/test（ldflags version=git describe）
```

## 4. 数据模型（master SQLite，`internal/master/store/`）

- **agents**：id/name/token(安装)/secret(鉴权)/enabled/created_at/last_seen_at/last_ip/version/pending_upgrade/**country**/**arch**
- **policies**：agent_id/enabled/interval_sec/size_mb/**size_min_mb**/**size_max_mb**(随机区间)/updated_at
- **stats**：agent_id/bytes_up/upload_count/last_upload_at
- **upload_events**：id/agent_id/ts/bytes/status(ok/fail)/error/**duration_ms**
- **settings**：key/value（admin_password_hash(bcrypt) / tunnel_enabled / gh_proxy）
- **migrate.go**：`ALTER ADD COLUMN` 兼容旧库（PRAGMA table_info 检查），新增字段不丢数据

## 5. Master API

**agent-facing**（Bearer agent.secret）：
- `POST /api/agent/register` {token} → {agent_id, secret}
- `POST /api/agent/{id}/heartbeat`（header X-Agent-Version/-Country/-Arch）→ {ok, upgrade_to?, download_url?}（升级指令 + 地区感知下载地址）
- `GET /api/agent/{id}/policy` → {enabled, interval_sec, size_mb, size_min_mb, size_max_mb, upload_url}（upload_url=tunnel 地址，空则 agent 回退 --server）

**panel-facing**（admin session token）：
- `POST /api/login` / `POST /api/password`（改密，bcrypt）
- `GET /api/nodes`（含 latest_version、每节点 `upload_stale/upload_stale_sec`）/ `POST /api/nodes`（建节点 + 生成 install 命令）
- `PUT /api/nodes/{id}/policy` / `GET /api/nodes/{id}/events`(24h) / `GET /api/nodes/{id}/install-command` / `POST /api/nodes/{id}/upgrade`(设 pending) / `DELETE /api/nodes/{id}`
- `GET|POST /api/tunnel`（status/enable）+ `POST /api/tunnel/disable`（cloudflared 管理）
- `GET|POST /api/gh-proxy`（加速源配置）

**数据面**：`PUT /upload/{agent_id}`（Bearer secret，io.Discard 计数）

## 6. Agent（多 master）

- **state v2**：`{version:2, masters:[{server, agent_id, secret, stopped}]}`（旧 v1 `{agent_id,secret}` 自动迁移，server 取 Config.Server 兜底）。dedup key = normalized `scheme://host[:port]`
- **masterConn**（per-master）：register/heartbeat/refreshPolicy/upload 独立；upload 日志带目标地址 `[<base>/upload/<id>] uploaded N`
- **Agent supervisor**：masters map + cancelers + `reconcile`（SIGHUP 触发，读 state 文件 diff → start/stop）；`selfUpgrade` 全局 gate（单 binary）
- **CLI**（`cmd/agent/main.go` os.Args[1] 分发）：`run`(daemon)/`add --server --token`(同 server 覆盖凭证)/`list`/`remove`/`stop`/`start`；**legacy 兼容**（无子命令→旧扁平 --server/--token，空 state 等价 add+run）
- **reload**：`systemctl reload`(ExecReload=kill -HUP)→SIGHUP→reconcile（热加/热删/热停）
- **地区**：`detectCountry()`(ip-api.com) + heartbeat 上报；master 据此决定自升级 download_url（CN+gh_proxy→加速，否则直连）

## 7. 部署

**Master**（国内/境外均可；面板 + 接收 + 可选 tunnel）：
- docker compose（Caddy 自动 HTTPS，需域名）或 二进制 systemd（HTTP 测试）
- 环境变量：`MASTER_ADDR`/`MASTER_DB`/`MASTER_BASE_URL`/`MASTER_ADMIN_PASSWORD`
- **CF Tunnel**（面板开关）：装 cloudflared + quick tunnel（trycloudflare URL，**强制 --protocol quic**），master 重启自动恢复（settings tunnel_enabled）。未备案国内机用 tunnel 绕开 443 备案检查

**Agent**（被保活 VPS）：
- `install.sh`（raw main，国内默认走 gh-proxy.org）：装 binary + systemd unit（ExecStart=`run --state` + ExecReload）+ `agent add` 添加首个 master
- 加多个 master：跑另一 master 的 install 命令（= add）或 `traffic-keeper-agent add --server --token`
- 自升级：master 面板点升级 → agent heartbeat 拿 upgrade_to + download_url → 自下载替换（gh-proxy 走加速）+ systemctl restart

## 8. 开发进展（v0.1 → v0.9.004）

| 版本 | 内容 |
|---|---|
| v0.1.0 | 项目骨架 |
| v0.2.0 | master 控制面/数据面/存储 + agent 注册/心跳/上传（端到端） |
| v0.3.0 | 一行安装(install.sh) + CI 发版 + Caddy/docker 部署；修 master 前端 embed |
| v0.3.x | agent 三 goroutine（上传不阻塞心跳）+ 前端硬刷新 401 修复 + CF Tunnel（v0.3 验证 5MB 成功） |
| v0.4.0 | 随机流量(size 区间) + agent 版本上报/自升级 + 24h 曲线 + 图标操作 + install.sh gh-proxy 默认 |
| v0.5.0 | acme UI 重做(Inter/lucide/token) + tunnel 持久化自恢复 + 流量类型 segmented |
| v0.6.0 | 加速源面板配置 + 地区感知(ip-api) + 修自升级 ProtectSystem 只读 |
| v0.7.0 | agent 多 master(一发多收) + CLI(add/list/remove/stop/start) + 上传日志带地址 + install.sh add 模式 |
| (已合) | cloudflared 强制 --protocol quic（QUIC 被 GFW 间歇干扰致降级 HTTP/2→524；强制 quic 后 1MB 从 50s→1s）—— ⚠️结论已于 v0.9.2 推翻 |
| v0.8.0 | 仪表盘页(导航+KPI+24h趋势+成功率+地区分布+平均速率) + 随机上传间隔(interval 区间) + 列表加宽/手动刷新/按钮上移 + EOF 自动恢复(上传失败刷新策略+tunnel未就绪退避+master关闭Disable tunnel) |
| v0.8.1 | agent 把 5xx 也当上传失败(30s 退避+刷新策略)——线上 master 重启期是 CF 5xx 而非 EOF，v0.8.0 的 err 触发没覆盖 |
| v0.9.0/v0.9.1 | cloudflared 优选 edge IP（TCP:443 测延迟/丢包 + 手动应用 --edge 注入 + 面板；v0.9.1 修 ctx 取消+.0 地址 bug） |
| **v0.9.2** | **默认协议改 http2**：线上实测 GFW 现严重干扰 UDP 7874(quic) 但放行 TCP 443——quic 5MB/95s 超时(55KB/s) vs http2 5MB/4.6s(1.1MB/s，agent 实战 620KB/s)，**~20x**。**推翻"QUIC>HTTP2"旧结论**；优选 IP 仅 quic 模式辅助(http2 不需要) |
| v0.9.003 | web token 过期自动回登录页（axios 401 拦截）+ `deploy/manage.sh` 一键管理脚本（装/更/卸 master & agent；curl\|bash 下交互 read 必须 `</dev/tty`）+ README 精简 |
| **v0.9.004** | **tunnel 自愈 + 上传停滞哨兵**（08-14 事故加固，见第 10 节坑 11）：连续 `Tunnel not found`≥3 → 自动重启 cloudflared 重拿 URL（10min 冷却），agent ≤90s 自动跟上、全链路无人值守；`/api/nodes` 增 `upload_stale/_sec`（在线但超阈值无上传，阈值=3×最长间隔+5min、下限 45min）+ 节点列表"在线·停滞Xm"角标 + 后台 5min 哨兵打 ERROR 日志 |

## 9. 当前线上状态

- **Master**：线上 Linux/amd64，systemd，HTTP :8080，**v0.9.004，走 release+gh-proxy 部署**（`manage.sh update-master`）；服务器地址和登录凭据只保存在本地忽略文件，不纳入仓库。2026-07-30 重装过系统。
- **Agent**：5 台（美国RN主站 / OR日本 / BA香港 / OR韩国 / OR美国）均 **v0.9.003**，走 CF Tunnel（**http2**）上传
- tunnel 已开，**默认 http2**（v0.9.2，实测 ~620KB/s；settings 持久化，master 重启自动恢复）；v0.9.004 起 **SelfHealLoop 自动救活被注销的 tunnel**（journal 搜 `tunnel: self-heal`）

## 10. 关键坑/决策（务必读）

1. **国内 GitHub releases 被墙**：install.sh/自升级/cloudflared 下载走 `gh-proxy.org`（面板可配）；raw.githubusercontent.com 通（install.sh 本身能拉，~5min CDN 缓存，改后用 commit-SHA URL 绕过）
2. **国内未备案**：域名 443 HTTPS 被机房 SNI 拦；CF Tunnel 不开入站端口故绕开（master 主动出站建 tunnel）
3. **CF Tunnel 524 / 协议选择（v0.9.2 推翻旧结论！）**：源站 100s 超时。**历史判断"强制 `--protocol quic` 解决（QUIC 通则快）"，但 2026-07-31 线上实测推翻**：GFW 现严重干扰 UDP 7874(quic)——quic 上传 5MB 要 95s 超时(55KB/s)、大量 524；而 **http2(TCP 443) 5MB 仅 4.6s(1.1MB/s)，agent 实战 620KB/s，快 ~20x**。v0.9.2 起默认 `--protocol http2`。**优选 IP(v0.9.0/v0.9.1) 仅 quic 模式辅助，http2 不需要**。size ≤5MB 防 100s 超时
4. **agent 自升级 ProtectSystem 只读**：install.sh systemd unit 的 `ProtectSystem=strict` 让 /usr 只读，selfUpgrade 写 `.new` 失败；修 `ReadWritePaths` 加二进制目录
5. **换 master / 重装 agent 必删 `/var/lib/traffic-keeper/agent.state.json`**：否则 agent 用旧 id/secret 心跳新 master → 全 401（日志 `resuming agent <旧id>`）
6. **ldflags 注入 version**：CI `${GITHUB_REF_NAME}`、Makefile `git describe`；master version = agent 自升级目标
7. **`make web` 必须执行**：只 `vite build` 不同步 `internal/web/dist`，master 服务旧 UI
8. **go 在 `~/.local/go/bin`**（本机，brew 国内卡改阿里云镜像），GOPROXY=goproxy.cn，npm=npmmirror
9. **master 升级后 agent 刷 EOF（v0.8.0 修复）**：根因=master 重启→systemd cgroup 杀 cloudflared 子进程→旧 trycloudflare URL 立即失效→agent 仍用缓存旧 URL PUT 被 Cloudflare 边缘 RST→`client.Do` 返回 EOF；且上传失败只 log 不刷新策略、过渡窗口 master 返回 `upload_url:""`→agent 回退 `--server` 直连被 GFW RST。手动重启 agent 走 startMaster 同步 refreshPolicy 恰好跳过窗口故"看起来要手动重拉"。修复=上传失败带节流(10s)异步 refreshPolicy + tunnel 启用但 URL 未就绪时 agent 跳过不回退直连(策略增 `tunnel_enabled`) + master 关闭主动 Disable tunnel
10. **QUIC 审查/封禁风险**：强制 QUIC 本身**不会**让 master IP 被 GFW 封禁——QUIC/UDP 到 Cloudflare 边缘是公开标准流量、CF IP 被海量合法服务共用，GFW 仅间歇丢包/QoS 干扰（即降级与 524），属稳定性问题非封禁。真正"被封/被审查"风险来自 master 暴露的 HTTP:8080 控制面（未备案+弱口令可被扫描爆破），与 QUIC 无关；trycloudflare 免费 quick tunnel 长期持续大流量可能触发 Cloudflare ToS 限制（CF 侧关 tunnel，非 GFW 封 master）。建议：控制面 8080 加来源白名单/防火墙；长期用自有域名 named tunnel 替代 trycloudflare；QUIC 出站限速规避运营商 QoS
11. **quick tunnel 会被 CF 注销（2026-08-14 实发，停传 3 天）**：坑 10 预警的 ToS 回收真实发生了——tunnel 跑 13 天后被 CF 侧注销，cloudflared 进程活着但持续 `Unauthorized: Tunnel not found`，master 无感知继续发死 URL，5 台 agent 上传全停而心跳正常（面板全"在线"，极具迷惑性）。**判别**：面板在线但仪表盘/日志无上传 → 查 `GET /api/tunnel` logs 有无 `Registered`/`Tunnel not found`；upload_events 连 fail 都没有 = 请求死在 CF 边缘。**修复**：面板 disable→enable 重拿新 URL（注意 **enable 是 `POST /api/tunnel`**，无 `/enable` 后缀；每次重启 URL 必变，agent ≤90s 自动跟上）。**v0.9.004 起自愈**：连续 not found ≥3 自动重启（10min 冷却），无需人工。治本 = named tunnel（见第 12 节）

## 11. 开发指南

```bash
export PATH="$HOME/.local/go/bin:$PATH"
export GOPROXY=https://goproxy.cn,direct
export npm_config_registry=https://registry.npmmirror.com

make test            # 全测试
make web             # 前端 build + stage 到 internal/web/dist
make build-master    # 含前端的 master（ldflags version=git describe）
make build-agent     # agent

# 发版：改代码 → commit → git tag v0.x.0 → push（CI 多架构发版）
# 部署 master：服务器 gh-proxy 拉 release 二进制（scp 间歇失败时用这招）
# 部署 agent：install.sh（面板生成命令）
```

**测试要点**：agent 多 master 测试用 `startFakeMaster`（真 store+master+httptest），interval 改 var 调小；store 用 t.TempDir() 真实 SQLite。

## 12. 后续可做（V1/V2）

**治本项：自有域名 named tunnel 替代 trycloudflare**（quick tunnel 会被 CF 随机回收，v0.9.004 自愈只是自动重接；named tunnel 有稳定 ID 不被注销，需一个托管在 CF 的域名 + 一次性 `cloudflared tunnel login`）；异常 webhook 通知（Telegram/飞书，哨兵触发时推送）。

随机化时间抖动、WebSocket 实时指令（现拉取式启停 ≤30s）、分组批量、代理链(WARP/SOCKS5)、流量伪装、多目标上传。

---

## 13. v0.10 自动化与 APK 来源管理（新增需求）

### 13.1 产品定义

左侧导航新增「自动化」「应用来源」，原「节点管理」改名「对端节点」。两类流量逻辑彼此独立：

- **对端节点上传**：Agent 按每条 Master 连接的策略向对应 Master 上传数据。每个对端节点/连接有独立上传开关；关停上传后保留注册、心跳、策略同步和自升级。此开关不影响其他 Master，也不影响自动化。
- **自动化下载**：Agent 在本机 Linux VPS 读取网卡计数，统计整机所选网卡的 RX（下行）与 TX（上行）。配置仅为「下行目标达到上行的 X 倍」。当统计窗口内下行不足目标时，在随机检查间隔中下载一个随机选择的 APK 来源，以补足下行。自动化是 Agent 级任务，不归属任何对端节点。

两类任务可同时启用、只启用其中一种或全部暂停。自动化默认关闭，升级 Agent 后不得自行开始下载。

### 13.2 比例、采样和调度

- 目标公式：`窗口下行目标 = 窗口上行 × target_multiplier`。例如倍数 1.3、上行 1GB、下行 0.8GB，估算缺口约 0.5GB。比例是下行相对上行，无需额外设置上传量。
- 默认统计窗口为滚动 24 小时。Agent 读取 `/sys/class/net/<iface>/statistics/rx_bytes` 和 `tx_bytes`，周期保存带时间戳的快照，以计算窗口差值；保留至少 8 天采样。无历史基线、计数下降/重置或网卡不可读时应跳过并记录状态，不得把启动以来总量误说为滚动窗口量。
- 可配置网卡名；留空表示汇总非 loopback 接口。建议用户选择默认路由物理网卡，避免 Docker/veth/bridge 与物理接口重复计数。网卡计数包含同接口上的其他进程流量，不仅是 Traffic Keeper。
- 检查间隔可随机化。每轮随机抽选来源，单任务下载大小不得超过全局单任务最大 MB 和来源自身上限；不提供每日总量限额。达到比例后不启动新任务，当前任务允许完成；暂停时提供立即取消选择。
- 下载 worker 与 heartbeat、对端上传、策略拉取、自升级解耦，下载失败不得阻塞其他循环。

### 13.3 应用来源管理

新增独立「应用来源」页面，管理员可以：

- 查看/新增/编辑/启停/删除来源，字段包括唯一 ID、应用名称、厂商、HTTPS 下载地址、允许的主机名/重定向主机、单任务最大 MB、官网/自定义标记。
- 手动录入其他来源；仅允许 HTTPS。自定义地址由管理员自行确认版权、授权与安全性。
- 点「测试地址」执行有超时和受控重定向的探测，显示最终地址、HTTP 状态、Content-Type、文件长度及 APK 魔数校验结果。测试不得下载完整文件；无法确认 APK 时明确警告。
- 自动化设置中只可启用已开启的来源。来源策略下发至 Agent 时只包含选择的来源。
- 预置官方入口/地址需经直接官网抓取与现场验证后才纳入。搜索结果、应用商店详情页、扫码落地页不能当作 APK 直链。未能确认稳定直链的应用可作为未启用候选，标注官网入口/待确认；不伪造直链。

首批核查候选包括微信、QQ、腾讯 WorkBuddy、淘宝、支付宝、闲鱼、京东、美团/大众点评等；实现时逐条核实官网页面、实际响应、HTTPS、跳转域名、响应类型、大小/哈希。官网仅提供二维码或应用商店跳转时，保留为官网入口但不启用自动下载。

### 13.4 下载安全

- 仅 Agent 执行 APK 下载，写入安全临时文件并在任务结束删除；不得安装或运行 APK。
- URL 必须为 HTTPS；源站和每个重定向目标必须精确匹配来源允许主机列表，最多 5 次跳转。
- 连接超时、最大文件大小、HTTP 成功状态、APK/二进制响应类型均校验；下载文件需通过 APK ZIP 魔数校验。超限即终止并清理。
- 来源 URL 来自管理员控制面，应避免任意内网/本地地址 SSRF；拒绝 IP literal、localhost、私有网段 DNS 解析结果，且重定向每跳重新校验地址与主机白名单。
- 定期删除执行记录，保留最近 30 天。

### 13.5 UI / API / Agent 变化

- 对端节点列表增加每节点上传开关；数据库 `policies.upload_enabled` 迁移默认 true，保留现有策略 `enabled` 字段语义。
- 自动化页展示开关、网卡、采样窗口、上/下行、目标倍数、随机检查范围、单任务大小、来源选择及最近执行记录。记录字段为时间、应用/厂商、字节、结果、用时、原因，不显示对端节点列。
- 应用来源页支持 CRUD 和地址测试。
- Master API：`GET/PUT /api/automation`、`GET/POST /api/automation/sources`、`DELETE /api/automation/sources/{id}`、`POST /api/automation/source-test`、`GET /api/automation/events`。Agent 策略在 `/api/agent/{id}/policy` 中增加 `upload_enabled` 和 `automation`；Agent 回报 `POST /api/agent/automation/event`。
- 多 Master 决策：Agent 状态文件中首个未暂停 Master 是自动化唯一控制源；该连接被暂停或移除后，下一个未暂停 Master 接管。上传开关仍逐 Master 独立。
- Agent state v2 增加采样快照字段，旧格式自然迁移；Master SQLite 以 `ALTER ADD COLUMN` 兼容旧库。

### 13.6 验收

1. 导航显示仪表盘、对端节点、自动化、应用来源；老节点数据与功能保留。
2. 每条 Master 上传策略可单独暂停/恢复，心跳继续；其他 Master 和自动化不受影响。
3. 自动化独立开关默认关闭，与对端上传可同时或单独运行。
4. 网卡 RX/TX、窗口差值与目标倍数正确；无完整窗口基线/计数回退时不下载。
5. 下载只使用已启用来源；白名单重定向与 APK 校验生效；单任务硬限制生效；无每日限额。
6. APK 临时文件清理，下载任务不会阻塞 heartbeat 或上传。
7. 执行记录不包含对端节点字段，成功/失败/跳过均有可读状态。
8. 来源管理支持 CRUD 与测试地址，默认来源只有经官网抓取和验证的项目；官网入口待确认项目保持关闭。

### 13.7 开发实施状态

实现已启动：Master 存储/API、Agent worker 和面板初版已加入。微信、抖音、支付宝官网入口已作为未启用候选预置，但尚未现场验证稳定 APK 直链；QQ、WorkBuddy、淘宝、闲鱼、京东、美团/大众点评需逐项验证后再添加为可下载来源。
