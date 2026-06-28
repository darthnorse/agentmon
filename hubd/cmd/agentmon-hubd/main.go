package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"

	"agentmon/hubd/internal/api"
	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/webui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "user" {
		if err := runUserCmd(os.Args[2:]); err != nil {
			log.Fatalf("user: %v", err)
		}
		return
	}
	cfgPath := flag.String("config", "/data/config.yaml", "path to config.yaml")
	flag.Parse()

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	database, err := openDB(cfg)
	if err != nil {
		log.Fatalf("db: %v", err)
	}
	defer database.Close()

	reg := registry.New(cfg.Servers)
	store := authn.NewStore(cookieTTL(cfg))
	auth := &authn.Authenticator{Store: store, CookieName: cfg.SessionCookie.Name}
	rec := audit.NewRecorder(database)

	router := api.NewRouter(api.RouterDeps{
		Version:             version,
		Auth:                auth,
		TrustForwardedProto: cfg.TrustForwardedProto,
		Login: authn.LoginDeps{
			Users:               database,
			Store:               store,
			Limiter:             authn.NewLimiter(rateMax(cfg), rateWindow(cfg)),
			Audit:               rec,
			CookieName:          cfg.SessionCookie.Name,
			CookieTTL:           cookieTTL(cfg),
			ExternalOrigin:      cfg.ExternalOrigin,
			TrustForwardedProto: cfg.TrustForwardedProto,
		},
		API: api.Deps{
			Reg:           reg,
			Agent:         registry.NewClient(10 * time.Second),
			Audit:         rec,
			AuditRepo:     database,
			HealthTimeout: 3 * time.Second,
		},
		WebUI: webui.Handler(),
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// No WriteTimeout: M4's long-lived terminal WS relay must not be killed by
		// a global write deadline (it uses per-message deadlines instead).
	}
	log.Printf("agentmon-hubd %s listening on %s (%d servers)", version, cfg.Listen, len(cfg.Servers))
	log.Fatal(srv.ListenAndServe())
}

func openDB(cfg config.Config) (*db.DB, error) {
	dir := cfg.DataDir
	if dir == "" {
		dir = "."
	}
	return db.Open(dir + "/agentmon.sqlite")
}

func cookieTTL(cfg config.Config) time.Duration {
	if cfg.SessionCookie.TTL > 0 {
		return cfg.SessionCookie.TTL
	}
	return 168 * time.Hour
}

func rateMax(cfg config.Config) int {
	if cfg.LoginRateLimit.MaxAttempts > 0 {
		return cfg.LoginRateLimit.MaxAttempts
	}
	return 5
}

func rateWindow(cfg config.Config) time.Duration {
	if cfg.LoginRateLimit.Window > 0 {
		return cfg.LoginRateLimit.Window
	}
	return 15 * time.Minute
}

// runUserCmd implements: agentmon-hubd user set-password --username <u> [--display <d>] [--config <path>]
// The password is read from the AGENTMON_PASSWORD env var, or from stdin if unset.
func runUserCmd(args []string) error {
	if len(args) < 1 || args[0] != "set-password" {
		return fmt.Errorf("usage: agentmon-hubd user set-password --username <u>")
	}
	fs := flag.NewFlagSet("set-password", flag.ExitOnError)
	username := fs.String("username", "", "username")
	display := fs.String("display", "", "display name (defaults to username)")
	cfgPath := fs.String("config", "/data/config.yaml", "path to config.yaml")
	fs.Parse(args[1:]) //nolint:errcheck // ExitOnError never returns an error
	if *username == "" {
		return fmt.Errorf("--username is required")
	}
	pw := os.Getenv("AGENTMON_PASSWORD")
	if pw == "" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return fmt.Errorf("reading password from stdin: %w", err)
		}
		pw = strings.TrimRight(string(b), "\r\n")
	}
	if pw == "" {
		return fmt.Errorf("empty password (set AGENTMON_PASSWORD or pipe via stdin)")
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	hash, err := authn.HashPassword(pw)
	if err != nil {
		return err
	}
	dn := *display
	if dn == "" {
		dn = *username
	}
	if err := database.SetPassword(context.Background(), uuid.NewString(), *username, dn, hash); err != nil {
		return err
	}
	log.Printf("password set for user %q", *username)
	return nil
}
