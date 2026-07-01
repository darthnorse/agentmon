package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	webpush "github.com/SherClockHolmes/webpush-go"

	"agentmon/hubd/internal/api"
	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/authn"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/directive"
	"agentmon/hubd/internal/registry"
	"agentmon/hubd/internal/state"
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
	if len(os.Args) > 1 && os.Args[1] == "server" {
		if err := runServerCmd(os.Args[2:]); err != nil {
			log.Fatalf("server: %v", err)
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

	reg := registry.New(database)
	proj := state.NewProjection()
	agentClient := registry.NewClient(10 * time.Second)
	store := authn.NewStore(cookieTTL(cfg))
	auth := &authn.Authenticator{Store: store, CookieName: cfg.SessionCookie.Name}
	rec := audit.NewRecorder(database)

	// First-run bootstrap: seed a default login ONLY when the DB has no users yet, so
	// a fresh hub is reachable immediately (the login UI then nudges changing it). A
	// failed seed leaves the hub unusable, so fail loud. The default password is NOT
	// logged (it's a known constant, but credentials don't belong in system logs).
	if hash, herr := authn.HashPassword(authn.DefaultPassword); herr != nil {
		log.Fatalf("hash default password: %v", herr)
	} else if seeded, serr := database.SeedDefaultUser(context.Background(), uuid.NewString(),
		authn.DefaultUsername, authn.DefaultUsername, hash); serr != nil {
		log.Fatalf("seed default user: %v", serr)
	} else if seeded {
		log.Printf("seeded default login %q — change it in the web UI (⚙ Settings) or via 'user set-password'",
			authn.DefaultUsername)
	}

	onboard := authn.NewLimiter(enrollMax(cfg), enrollWindow(cfg))

	bcast := state.NewBroadcaster()
	poller := state.NewPoller(reg, agentClient, database, proj, statePoll(cfg), time.Now, bcast)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go poller.Run(ctx)

	// M9: Web-Push. Load (or generate-on-first-boot) the persisted VAPID keypair,
	// track live-SSE presence for server-side push de-dup, and run the dispatcher
	// as a broadcaster subscriber over the same ctx lifetime as the poller.
	presence := state.NewPresence()
	vapid, err := database.LoadOrCreateVAPID(ctx, webpush.GenerateVAPIDKeys, state.HubTS(time.Now()))
	if err != nil {
		log.Fatalf("vapid: %v", err)
	}
	go state.RunPushDispatcher(ctx, state.DispatcherDeps{
		Bcast:      bcast,
		Presence:   presence,
		Store:      database,
		Send:       state.NewWebPushSender(vapid, vapidSubject(cfg)),
		NowRFC3339: func() string { return time.Now().UTC().Format(time.RFC3339) },
	})

	relayCap := authn.NewGauge(32) // Phase 5: ≤32 concurrent terminal relays per principal

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
		Password: authn.PasswordDeps{
			Users:               database,
			Audit:               rec,
			Store:               store,
			CookieName:          cfg.SessionCookie.Name,
			TrustForwardedProto: cfg.TrustForwardedProto,
		},
		API: api.Deps{
			Reg:                 reg,
			Agent:               agentClient,
			Audit:               rec,
			AuditRepo:           database,
			HealthTimeout:       3 * time.Second,
			TrustForwardedProto: cfg.TrustForwardedProto,
			Minter:              directive.Minter{}, // defaults: time.Now, CSPRNG nonce, uuid requestId
			ExternalOrigin:      cfg.ExternalOrigin,
			Proj:                proj,
			Seen:                database,
			Bcast:               bcast,
			SSEHeartbeat:        sseHeartbeat(cfg),
			Push:                database,
			VAPIDPublic:         vapid.Public,
			Presence:            presence,
			RelayCap:            relayCap,
		},
		Enroll:  api.EnrollDeps{Servers: database, Audit: rec, TrustForwardedProto: cfg.TrustForwardedProto},
		Onboard: onboard,
		Install: api.InstallDeps{HubURL: cfg.ExternalOrigin},
		WebUI:   webui.Handler(),
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
	if strings.HasPrefix(cfg.ExternalOrigin, "https://") && !cfg.TrustForwardedProto {
		log.Printf("WARNING: external_origin is HTTPS but trust_forwarded_proto is false — session cookies will be issued WITHOUT the Secure flag. Behind Caddy/TLS set trust_forwarded_proto: true.")
	}
	log.Printf("agentmon-hubd %s listening on %s", version, cfg.Listen)
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("listen: %v", err)
		}
	}()
	<-ctx.Done()
	stop()
	shutCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutCtx)
}

func openDB(cfg config.Config) (*db.DB, error) {
	dir := cfg.DataDir
	if dir == "" {
		dir = "."
	}
	return db.Open(filepath.Join(dir, "agentmon.sqlite"))
}

func cookieTTL(cfg config.Config) time.Duration {
	if cfg.SessionCookie.TTL > 0 {
		return cfg.SessionCookie.TTL
	}
	return 168 * time.Hour
}

func statePoll(cfg config.Config) time.Duration {
	if cfg.StatePollInterval > 0 {
		return cfg.StatePollInterval
	}
	return 3 * time.Second
}

// vapidSubject returns the VAPID JWT subject (a mailto:/URL contact the protocol
// requires). It prefers the explicit config value, falls back to the configured
// external origin, and finally to a placeholder so webpush never sends an empty
// subscriber.
func vapidSubject(cfg config.Config) string {
	if cfg.VAPIDSubject != "" {
		return cfg.VAPIDSubject
	}
	if cfg.ExternalOrigin != "" {
		return cfg.ExternalOrigin
	}
	return "mailto:admin@localhost"
}

func sseHeartbeat(cfg config.Config) time.Duration {
	if cfg.SSEHeartbeat > 0 {
		return cfg.SSEHeartbeat
	}
	return 25 * time.Second
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

func enrollMax(cfg config.Config) int {
	if cfg.EnrollRateLimit.MaxAttempts > 0 {
		return cfg.EnrollRateLimit.MaxAttempts
	}
	return 30
}

func enrollWindow(cfg config.Config) time.Duration {
	if cfg.EnrollRateLimit.Window > 0 {
		return cfg.EnrollRateLimit.Window
	}
	return time.Minute
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
