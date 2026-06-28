package api

import (
	"net/http"

	"agentmon/hubd/internal/authn"
)

type RouterDeps struct {
	Version             string
	Auth                *authn.Authenticator
	Login               authn.LoginDeps
	TrustForwardedProto bool
	API                 Deps
	WebUI               http.Handler
}

func NewRouter(rd RouterDeps) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", HealthHandler(rd.Version))

	mux.Handle("POST /api/v1/auth/login", rd.Login.LoginHandler())
	mux.Handle("POST /api/v1/auth/logout", rd.Auth.RequireAuth(rd.Auth.LogoutHandler(rd.TrustForwardedProto)))
	mux.Handle("GET /api/v1/me", rd.Auth.RequireAuth(rd.Auth.MeHandler()))

	mux.Handle("GET /api/v1/servers", rd.Auth.RequireAuth(rd.API.ServersHandler()))
	mux.Handle("GET /api/v1/servers/{id}", rd.Auth.RequireAuth(rd.API.ServerHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions", rd.Auth.RequireAuth(rd.API.ServerSessionsHandler()))
	mux.Handle("GET /api/v1/servers/{id}/sessions/{name}", rd.Auth.RequireAuth(rd.API.SessionDetailHandler()))
	mux.Handle("GET /api/v1/audit", rd.Auth.RequireAuth(rd.API.AuditHandler()))

	mux.Handle("/", rd.WebUI)
	return mux
}
