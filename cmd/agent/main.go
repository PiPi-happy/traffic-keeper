// Command agent runs the traffic-keeper agent on a managed VPS.
//
// On first run it registers with the master using the install token, then
// persists its credentials. Subsequent runs resume. It loops: heartbeating,
// pulling its policy, and uploading random data to keep the host's upstream
// traffic non-zero.
//
// Flags (or env):
//
//	-server   TK_SERVER   master base URL, e.g. https://master.example.com
//	-token    TK_TOKEN    one-time install token (first run only)
//	-state    TK_STATE    credentials file (default ./traffic-keeper-agent.state.json)
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/PiPi-happy/traffic-keeper/internal/agent"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	var (
		server = flag.String("server", envOr("TK_SERVER", ""), "master base URL")
		token  = flag.String("token", envOr("TK_TOKEN", ""), "install token (first-run registration)")
		state  = flag.String("state", envOr("TK_STATE", "./traffic-keeper-agent.state.json"), "credentials file")
	)
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	a := agent.New(agent.Config{Server: *server, Token: *token, State: *state, Version: version})
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
