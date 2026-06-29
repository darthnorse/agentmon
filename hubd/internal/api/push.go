package api

import (
	"encoding/json"
	"net/http"
	"time"

	"agentmon/hubd/internal/authz"
	"agentmon/hubd/internal/db"
	"agentmon/hubd/internal/state"
)

// subscribeRequest is the Web-Push subscription JSON sent by the browser
// (the shape of PushSubscription.toJSON()).
type subscribeRequest struct {
	Endpoint string `json:"endpoint"`
	Keys     struct {
		P256dh string `json:"p256dh"`
		Auth   string `json:"auth"`
	} `json:"keys"`
}

type unsubscribeRequest struct {
	Endpoint string `json:"endpoint"`
}

// VapidHandler handles GET /api/v1/push/vapid: returns the server's VAPID public
// key (non-secret) so the client can call pushManager.subscribe.
func (d Deps) VapidHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"publicKey": d.VAPIDPublic})
	}
}

// SubscribeHandler handles POST /api/v1/push/subscribe: upserts a Web-Push
// subscription for the authenticated principal (keyed by endpoint).
func (d Deps) SubscribeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req subscribeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Endpoint == "" || req.Keys.P256dh == "" || req.Keys.Auth == "" {
			writeJSONError(w, http.StatusBadRequest, "endpoint and keys required")
			return
		}
		p, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*")
		if !ok {
			return
		}
		if err := d.Push.UpsertSubscription(r.Context(), db.PushSubscription{
			PrincipalID: p.ID,
			Endpoint:    req.Endpoint,
			P256dh:      req.Keys.P256dh,
			Auth:        req.Keys.Auth,
			UserAgent:   r.UserAgent(),
			CreatedAt:   state.HubTS(time.Now()),
		}); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}

// UnsubscribeHandler handles POST /api/v1/push/unsubscribe: deletes the
// subscription with the given endpoint. Idempotent (deleting an absent endpoint
// is still 204).
func (d Deps) UnsubscribeHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req unsubscribeRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil {
			writeJSONError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Endpoint == "" {
			writeJSONError(w, http.StatusBadRequest, "endpoint required")
			return
		}
		if _, ok := d.authorizeOr403(w, r, authz.ServerView, "server:*"); !ok {
			return
		}
		if err := d.Push.DeleteSubscription(r.Context(), req.Endpoint); err != nil {
			writeJSONError(w, http.StatusInternalServerError, "internal error")
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}
}
