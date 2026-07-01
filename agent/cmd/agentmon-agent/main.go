package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"
	"time"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
	"agentmon/agent/internal/directive"
	"agentmon/agent/internal/hooks"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

var version = "dev"

// newAgentServer builds the agent's HTTP server with Slowloris/hygiene timeouts.
// These mirror the hub's (hubd/cmd/agentmon-hubd/main.go) and are verified
// WS-safe there: after the pane-IO Upgrade the conn is hijacked, so ReadTimeout
// no longer applies. There is deliberately NO WriteTimeout — a global write
// deadline would kill the long-lived terminal WS mid-stream.
func newAgentServer(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
	}
}

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "hooks":
			if err := hooksMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
		case "hook-test":
			if err := hookTestMain(os.Args[2:], os.Stdout); err != nil {
				log.Fatal(err)
			}
			return
		}
	}

	cfgPath := flag.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.HubToken == "" {
		log.Fatal("config: hub_token is required")
	}
	if cfg.DirectiveKey == "" {
		log.Fatal("config: directive_key is required")
	}

	discover := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return tmux.Discover(ctx, tmux.ExecRunner, opts)
	}

	createSession := func(ctx context.Context, socket, name, cwd string) error {
		return tmux.CreateSession(ctx, tmux.ExecRunner, socket, name, cwd)
	}
	renameSession := func(ctx context.Context, socket, from, to string) error {
		return tmux.RenameSession(ctx, tmux.ExecRunner, socket, from, to)
	}
	killSession := func(ctx context.Context, socket, name string) error {
		return tmux.KillSession(ctx, tmux.ExecRunner, socket, name)
	}

	machine := state.New(nil)

	_, tmuxErr := exec.LookPath("tmux")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version, tmuxErr == nil))
	mux.Handle("GET /sessions", api.RequireBearer(cfg.HubToken, api.SessionsHandler(cfg, discover, machine)))
	mux.Handle("POST /sessions", api.RequireBearer(cfg.HubToken, api.CreateSessionHandler(cfg, createSession)))
	mux.Handle("POST /sessions/rename", api.RequireBearer(cfg.HubToken, api.RenameSessionHandler(cfg, renameSession)))
	mux.Handle("POST /sessions/kill", api.RequireBearer(cfg.HubToken, api.KillSessionHandler(cfg, killSession)))
	mux.Handle("GET /state", api.RequireBearer(cfg.HubToken, api.StateHandler(cfg, machine)))

	paneIO := &api.PaneIO{
		Cfg:      cfg,
		Verifier: directive.NewVerifier(cfg.ServerID, []byte(cfg.DirectiveKey), nil),
		Run:      tmux.ExecRunner,
		Capture:  tmux.CapturePane,
		NewClient: func(ctx context.Context, socket, session, pane string) (api.PaneConn, error) {
			return tmux.NewControlClient(ctx, socket, session, pane)
		},
		Tune: tmux.TuneSession,
	}
	mux.Handle("GET /panes/{paneId}/io", api.RequireBearer(cfg.HubToken, paneIO.Handler()))

	if cfg.HookToken != "" {
		if cfg.HookTokenFile != "" {
			if err := hooks.WriteTokenFile(cfg.HookTokenFile, cfg.HookToken); err != nil {
				log.Fatalf("hook token file: %v", err)
			}
		}
		mux.Handle("POST /hook", hooks.RequireLoopback(hooks.RequireHookAuth(cfg.HookToken, hooks.HookHandler(cfg, machine, nil))))
		log.Printf("hook intake enabled at POST /hook")
	}

	log.Printf("agentmon-agent %s listening on %s (server %s)", version, cfg.Listen, cfg.ServerID)
	srv := newAgentServer(cfg.Listen, mux)
	log.Fatal(srv.ListenAndServe())
}
