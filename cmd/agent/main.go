// Command agent runs the traffic-keeper agent, which can fan out to multiple
// masters ("一发多收"). Subcommands manage the master list; the running daemon
// reloads (add/remove/stop/start) on SIGHUP.
//
//	traffic-keeper-agent run                       # daemon (systemd)
//	traffic-keeper-agent add --server URL --token T
//	traffic-keeper-agent list
//	traffic-keeper-agent remove <server>
//	traffic-keeper-agent stop <server>
//	traffic-keeper-agent start <server>
//
// Legacy call (no subcommand) preserves the old flat --server/--token/--state
// flags so already-deployed systemd units keep working until install.sh reruns.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/PiPi-happy/traffic-keeper/internal/agent"
)

// version is injected at build time via -ldflags "-X main.version=...".
var version = "dev"

func main() {
	if len(os.Args) < 2 || strings.HasPrefix(os.Args[1], "-") {
		legacyRun()
		return
	}
	switch os.Args[1] {
	case "run":
		cmdRun(os.Args[2:])
	case "add":
		cmdAdd(os.Args[2:])
	case "list":
		cmdList(os.Args[2:])
	case "remove":
		cmdRemove(os.Args[2:])
	case "stop":
		cmdStop(os.Args[2:])
	case "start":
		cmdStart(os.Args[2:])
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "unknown command: %s\n", os.Args[1])
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `Usage: traffic-keeper-agent <command> [flags]

Commands:
  run                          Run all configured, non-stopped masters (systemd)
  add  --server URL --token T  Add/overwrite a master (same server overwrites)
  list                         List configured masters
  remove <server>              Remove a master
  stop  <server>               Stop a master (keep config, resumable)
  start <server>               Resume a stopped master

Common flags:
  --state PATH   state file (env TK_STATE; default /var/lib/traffic-keeper/agent.state.json)
  --server URL   master base URL (env TK_SERVER; add / legacy run)
  --token T      install token (env TK_TOKEN; add / legacy run)`)
}

func defaultState() string {
	if v := os.Getenv("TK_STATE"); v != "" {
		return v
	}
	return "/var/lib/traffic-keeper/agent.state.json"
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func cmdRun(args []string) {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file")
	server := fs.String("server", envOr("TK_SERVER", ""), "master URL (only for legacy v1 state migration)")
	fs.Parse(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := agent.New(agent.Config{State: *state, Version: version, Server: *server})
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func cmdAdd(args []string) {
	fs := flag.NewFlagSet("add", flag.ExitOnError)
	server := fs.String("server", envOr("TK_SERVER", ""), "master base URL (required)")
	token := fs.String("token", envOr("TK_TOKEN", ""), "install token (required)")
	state := fs.String("state", defaultState(), "state file")
	fs.Parse(args)
	if *server == "" || *token == "" {
		fatal("add: --server and --token are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	id, err := agent.AddMaster(ctx, *state, *server, *token)
	if err != nil {
		fatal("add: " + err.Error())
	}
	agent.Reload()
	fmt.Printf("added master %s (agent %s)\n", agent.NormalizeServer(*server), id)
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file")
	fs.Parse(args)
	ms, err := agent.ListMasters(*state)
	if err != nil {
		fatal("list: " + err.Error())
	}
	if len(ms) == 0 {
		fmt.Println("(no masters configured)")
		return
	}
	fmt.Printf("%-42s %-34s %s\n", "SERVER", "AGENT_ID", "STATUS")
	for _, m := range ms {
		status := "running"
		if m.Stopped {
			status = "stopped"
		}
		fmt.Printf("%-42s %-34s %s\n", m.Server, m.AgentID, status)
	}
}

func cmdRemove(args []string) {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fatal("remove: requires <server>")
	}
	found, err := agent.RemoveMaster(*state, fs.Arg(0))
	if err != nil {
		fatal("remove: " + err.Error())
	}
	if !found {
		fatal("remove: master not found: " + agent.NormalizeServer(fs.Arg(0)))
	}
	agent.Reload()
	fmt.Println("removed")
}

func cmdStop(args []string)  { toggleStopped(args, "stop", true) }
func cmdStart(args []string) { toggleStopped(args, "start", false) }

func toggleStopped(args []string, name string, stopped bool) {
	fs := flag.NewFlagSet(name, flag.ExitOnError)
	state := fs.String("state", defaultState(), "state file")
	fs.Parse(args)
	if fs.NArg() < 1 {
		fatal(name + ": requires <server>")
	}
	found, err := agent.SetMasterStopped(*state, fs.Arg(0), stopped)
	if err != nil {
		fatal(name + ": " + err.Error())
	}
	if !found {
		fatal(name + ": master not found: " + agent.NormalizeServer(fs.Arg(0)))
	}
	agent.Reload()
	fmt.Println(name + " done")
}

// legacyRun preserves the old flat --server/--token/--state behavior so existing
// systemd units keep working until install.sh is re-run with the new scheme.
func legacyRun() {
	server := flag.String("server", envOr("TK_SERVER", ""), "master base URL")
	token := flag.String("token", envOr("TK_TOKEN", ""), "install token (first-run)")
	state := flag.String("state", defaultState(), "state file")
	flag.Parse()
	// Empty state + server + token → register one master then run.
	if *server != "" && *token != "" {
		if _, err := os.Stat(*state); os.IsNotExist(err) {
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			id, err := agent.AddMaster(ctx, *state, *server, *token)
			cancel()
			if err != nil {
				log.Fatalf("register: %v", err)
			}
			log.Printf("registered as agent %s", id)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	a := agent.New(agent.Config{State: *state, Version: version, Server: *server})
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent: %v", err)
	}
}

func fatal(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
