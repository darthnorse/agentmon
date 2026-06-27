package main

import (
	"context"
	"flag"
	"log"
	"net/http"

	"agentmon/agent/internal/api"
	"agentmon/agent/internal/config"
	"agentmon/agent/internal/tmux"
	"agentmon/shared"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	if cfg.HubToken == "" {
		log.Fatal("config: hub_token is required")
	}

	discover := func(ctx context.Context, opts tmux.DiscoverOpts) ([]shared.Session, error) {
		return tmux.Discover(ctx, tmux.ExecRunner, opts)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", api.HealthHandler(cfg.ServerID, version))
	mux.Handle("GET /sessions", api.RequireBearer(cfg.HubToken, api.SessionsHandler(cfg, discover)))

	log.Printf("agentmon-agent %s listening on %s (server %s)", version, cfg.Listen, cfg.ServerID)
	log.Fatal(http.ListenAndServe(cfg.Listen, mux))
}
