package main

import (
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"os/exec"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
	"agentmon/agent/internal/directive"
	"agentmon/agent/internal/hooks"
	"agentmon/agent/internal/state"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

var version = "dev"

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

	machine := state.New(nil)

	_, tmuxErr := exec.LookPath("tmux")
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version, tmuxErr == nil))
	mux.Handle("GET /sessions", api.RequireBearer(cfg.HubToken, api.SessionsHandler(cfg, discover, machine)))

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
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
