# Deploying the master

The master runs as two containers via Docker Compose:

- **master** — the Go server (control plane + data plane), SQLite at `/data`.
- **caddy** — automatic HTTPS (Let's Encrypt) reverse-proxy in front of master.

## Requirements

- A server with a **public IP** and **ports 80 + 443** reachable (Caddy needs
  80 for the ACME HTTP-01 challenge and 443 for traffic).
- A **domain** with an A record pointing at that server.
- Docker + Docker Compose.

## Steps

```bash
git clone https://github.com/PiPi-happy/traffic-keeper.git
cd traffic-keeper/deploy

cp .env.example .env
# edit .env: MASTER_DOMAIN, MASTER_BASE_URL, MASTER_ADMIN_PASSWORD

docker compose up -d --build
```

Caddy obtains the certificate on first request. Open:

```
https://<MASTER_DOMAIN>
```

Log in with the password from `MASTER_ADMIN_PASSWORD`.

## Add a sender VPS

1. In the panel, click **New Node** → name it → copy the generated command.
2. On the target VPS: paste + run. The installer downloads the agent, writes a
   systemd service, and starts it. The agent registers itself automatically.

## Operations

```bash
docker compose logs -f          # follow logs
docker compose restart master   # restart master
docker compose pull && docker compose up -d --build   # update
```

The SQLite database persists in the `master-data` volume.

## Notes

- The agent installer is at `deploy/install.sh` (served via `raw.githubusercontent.com`).
- Agent binaries are published to GitHub Releases on every `v*` tag (see
  `.github/workflows/release.yml`). `install.sh` pulls the latest.
