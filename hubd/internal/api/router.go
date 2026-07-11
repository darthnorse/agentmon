package api

import (
	"net/http"

	"agentmon/hubd/internal/authn"
)

type RouterDeps struct {
	Version             string
	Auth                *authn.Authenticator
	Login               authn.LoginDeps
	Password            authn.PasswordDeps
	TrustForwardedProto bool
	API                 Deps
	Enroll              EnrollDeps
	Onboard             *authn.Limiter
	Install             InstallDeps
	WebUI               http.Handler
}

func NewRouter(rd RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(rd.Version))

	mux.Handle("POST /api/v1/auth/login", rd.Login.LoginHandler())
	mux.Handle("POST /api/v1/auth/logout", rd.Auth.RequireAuth(rd.Auth.LogoutHandler(rd.TrustForwardedProto)))
	mux.Handle("GET /api/v1/me", rd.Auth.RequireAuth(rd.Auth.MeHandler()))
	mux.Handle("POST /api/v1/auth/password", rd.Auth.RequireAuth(rd.Password.ChangeHandler()))

	mux.Handle("GET /api/v1/servers", rd.Auth.RequireAuth(rd.API.ServersHandler()))
	// Admit UI (M-admit): literal "pending" is more specific than "{id}", so ServeMux
	// routes /servers/pending here rather than to the {id} detail handler.
	mux.Handle("GET /api/v1/servers/pending", rd.Auth.RequireAuth(rd.API.PendingServersHandler()))
	mux.Handle("POST /api/v1/servers/{id}/approve", rd.Auth.RequireAuth(rd.API.ServerApproveHandler()))
	mux.Handle("POST /api/v1/servers/{id}/reject", rd.Auth.RequireAuth(rd.API.ServerRejectHandler()))
	mux.Handle("GET /api/v1/servers/{id}", rd.Auth.RequireAuth(rd.API.ServerHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions", rd.Auth.RequireAuth(rd.API.ServerSessionsHandler()))
	mux.Handle("POST /api/v1/servers/{id}/sessions", rd.Auth.RequireAuth(rd.API.ServerCreateSessionHandler()))
	mux.Handle("POST /api/v1/servers/{id}/sessions/rename", rd.Auth.RequireAuth(rd.API.ServerRenameSessionHandler()))
	mux.Handle("POST /api/v1/servers/{id}/sessions/kill", rd.Auth.RequireAuth(rd.API.ServerKillSessionHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions/{name}", rd.Auth.RequireAuth(rd.API.SessionDetailHandler()))
	mux.Handle("GET /api/v1/servers/{id}/panes/{paneId}/io", rd.Auth.RequireAuth(rd.API.PaneRelayHandler()))
	mux.Handle("GET /api/v1/audit", rd.Auth.RequireAuth(rd.API.AuditHandler()))
	mux.Handle("POST /api/v1/seen", rd.Auth.RequireAuth(rd.API.SeenHandler()))
	mux.Handle("GET /api/v1/events", rd.Auth.RequireAuth(rd.API.EventsHandler()))

	mux.Handle("GET /api/v1/push/vapid", rd.Auth.RequireAuth(rd.API.VapidHandler()))
	mux.Handle("POST /api/v1/push/subscribe", rd.Auth.RequireAuth(rd.API.SubscribeHandler()))
	mux.Handle("POST /api/v1/push/unsubscribe", rd.Auth.RequireAuth(rd.API.UnsubscribeHandler()))
	// Public by design (GitHub cannot hold cookies/CSRF); HMAC fails closed.
	// Deliberately NOT behind the onboard rate limiter: webhook bursts from a
	// busy repo would exceed it, and the pre-HMAC cost is one bounded (1MiB)
	// read + SHA-256.
	mux.Handle("POST /api/v1/github/webhook", rd.API.GitHubWebhookHandler())
	mux.Handle("GET /api/v1/orchestrator/projects", rd.Auth.RequireAuth(rd.API.OrchestratorProjectsHandler()))
	mux.Handle("POST /api/v1/orchestrator/projects", rd.Auth.RequireAuth(rd.API.OrchestratorProjectsHandler()))
	mux.Handle("GET /api/v1/orchestrator/projects/{id}/board", rd.Auth.RequireAuth(rd.API.OrchestratorBoardHandler()))
	mux.Handle("POST /api/v1/orchestrator/projects/{id}/actions", rd.Auth.RequireAuth(rd.API.OrchestratorActionsHandler()))
	mux.Handle("GET /api/v1/orchestrator/events", rd.Auth.RequireAuth(rd.API.OrchestratorEventsHandler()))

	mux.Handle("POST /api/v1/enroll", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Enroll.Handler()))

	mux.Handle("GET /install.sh", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Install.ScriptHandler()))
	mux.Handle("GET /dl/{file}", onboardRateLimit(rd.Onboard, rd.TrustForwardedProto, rd.Install.BinaryHandler()))

	// Unknown /api/ paths (and wrong methods on known ones) must not fall through to
	// the SPA. Registered /api/v1/... patterns are more specific and still win; this
	// only catches genuinely unknown API paths.
	mux.Handle("/api/", apiNotFound())

	mux.Handle("/", rd.WebUI)
	return mux
}

func apiNotFound() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeJSONError(w, http.StatusNotFound, "not found")
	})
}
