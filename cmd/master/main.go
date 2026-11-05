// Command master runs the traffic-keeper master server.
//
// It hosts the data plane (HTTP upload receiver that counts bytes and discards
// bodies) and the control plane (node management, policy dispatch, single-user
// auth). The web panel is served in a later task.
//
// Environment:
//
//	MASTER_ADDR            listen address        (default ":8080")
//	MASTER_DB              sqlite path           (default "traffic-keeper.db")
//	MASTER_BASE_URL        public HTTPS base URL, e.g. https://master.example.com
//	MASTER_ADMIN_PASSWORD  password for panel login
package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/PiPi-happy/traffic-keeper/internal/master"
	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

// version is injected at build time via -ldflags "-X main.version=...". It is
// used as the agent self-upgrade target (master and agent ship the same tag).
var version = "dev"

func main() {
	addr := envOr("MASTER_ADDR", ":8080")
	port := 8080
	if _, p, err := net.SplitHostPort(addr); err == nil {
		if n, e := strconv.Atoi(p); e == nil {
			port = n
		}
	} else if strings.HasPrefix(addr, ":") {
		if n, e := strconv.Atoi(strings.TrimPrefix(addr, ":")); e == nil {
			port = n
		}
	}
	dbPath := envOr("MASTER_DB", "traffic-keeper.db")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := master.NewServer(st,
		master.WithBaseURL(envOr("MASTER_BASE_URL", "")),
		master.WithAdminPassword(envOr("MASTER_ADMIN_PASSWORD", "")),
		master.WithVersion(version),
		master.WithListenPort(port),
	)
	if err := srv.InitAdminPassword(context.Background(), envOr("MASTER_ADMIN_PASSWORD", "")); err != nil {
		log.Fatalf("init admin password: %v", err)
	}
	httpSrv := &http.Server{Addr: addr, Handler: srv}

	go func() {
		log.Printf("traffic-keeper master listening on %s", addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("master server error: %v", err)
		}
	}()

	// Re-enable the tunnel if it was on before restart (new trycloudflare URL).
	srv.RestoreTunnelIntent(context.Background())

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("master shutting down...")
	_ = httpSrv.Shutdown(context.Background())
	srv.Stop() // cleanly stop cloudflared instead of letting systemd SIGKILL it
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
