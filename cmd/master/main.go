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
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/PiPi-happy/traffic-keeper/internal/master"
	"github.com/PiPi-happy/traffic-keeper/internal/master/store"
)

func main() {
	addr := envOr("MASTER_ADDR", ":8080")
	dbPath := envOr("MASTER_DB", "traffic-keeper.db")

	st, err := store.Open(dbPath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer st.Close()

	srv := master.NewServer(st,
		master.WithBaseURL(envOr("MASTER_BASE_URL", "")),
		master.WithAdminPassword(envOr("MASTER_ADMIN_PASSWORD", "")),
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

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop
	log.Println("master shutting down...")
	_ = httpSrv.Shutdown(context.Background())
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
