# Traffic Keeper

> 自用的 VPS 上行流量保活管控平台 —— 一个面板端（控制面 + 数据面接收端）统一纳管所有发送端 Agent。

![status](https://img.shields.io/badge/status-WIP%20(MVP)-orange) ![license](https://img.shields.io/badge/license-MIT-blue)

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

## MVP 范围

- ✅ Agent 注册（token）+ 心跳 + 按间隔/固定大小上传
- ✅ 面板：节点列表、累计上行、策略编辑（间隔 / 大小 / 开关）、一键生成安装命令
- ✅ 一行安装：`curl ... | bash`（GitHub Releases 多架构二进制 + systemd）
- ✅ HTTPS（Caddy 自动证书）

> 随机化（时间抖动 / 包大小范围）、WebSocket 实时指令、分组批量、代理链（WARP/SOCKS5）、流量伪装留待 V1 / V2。

## 技术栈

Go（Master + Agent 单二进制）· SQLite · Vue3 + Element Plus · Caddy · GitHub Actions

## 开发

```bash
go build ./...
go run ./cmd/master      # 面板端，监听 :8080
go run ./cmd/agent       # 发送端
```

## 状态

🚧 开发中 —— 当前：**项目骨架（Day 1）**。

## License

MIT
