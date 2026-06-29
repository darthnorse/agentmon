package state

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	webpush "github.com/SherClockHolmes/webpush-go"

	"agentmon/hubd/internal/db"
	"agentmon/shared"
)

// PushSender sends one encrypted Web-Push to a single subscription and returns
// the HTTP status reported by the push service (or 0 with a non-nil error on a
// transport failure). It is the dependency-injection seam the dispatcher uses so
// tests can drive it without real network/crypto; the production implementation
// is NewWebPushSender.
type PushSender func(ctx context.Context, sub db.PushSubscription, payload []byte) (status int, err error)

// PushDispatchStore is the subset of the push store the dispatcher needs.
// *db.DB satisfies it.
type PushDispatchStore interface {
	PrincipalIDsWithSubscriptions(ctx context.Context) ([]string, error)
	ListSubscriptionsForPrincipal(ctx context.Context, principalID string) ([]db.PushSubscription, error)
	DeleteSubscription(ctx context.Context, endpoint string) error
}

// DispatcherDeps bundles the collaborators for RunPushDispatcher.
type DispatcherDeps struct {
	Bcast      *Broadcaster
	Presence   *Presence
	Store      PushDispatchStore
	Send       PushSender
	NowRFC3339 func() string
}

// pushMsg is the decrypted JSON payload delivered to the service worker. Its
// shape is mirrored by the web service worker's push handler (M9 T8) and the
// web contracts. It carries only already-user-visible identifiers — no secrets.
type pushMsg struct {
	Type    string `json:"type"`
	Server  string `json:"server"`
	Target  string `json:"target"`
	Session string `json:"session"`
	Ts      string `json:"ts"`
}

// blockedGate remembers which sessions are CURRENTLY blocked so the dispatcher
// pushes only on a true transition INTO blocked — mirroring the web client's
// isAttentionTransition and the spec's one-push-per-blocked-episode rule. The
// poller can re-publish a blocked Change (a multi-pane session, or an epoch/
// committed re-emit) even when the session was already blocked; without this
// gate that would double-notify. It tracks only the set of blocked sessions
// (not every session's last state), so it is bounded by the number of
// concurrently-blocked sessions and self-prunes when a session leaves blocked.
// It is driven only from RunPushDispatcher's single drain goroutine, so it needs
// no synchronization.
type blockedGate struct{ blocked map[string]bool }

func newBlockedGate() *blockedGate { return &blockedGate{blocked: map[string]bool{}} }

// fresh records c and reports whether it is a NEW entry into blocked (the session
// was not already blocked). A non-blocked change forgets the session, so a later
// re-block is fresh again (a re-alert).
func (g *blockedGate) fresh(c Change) bool {
	key := c.ServerID + "\x1f" + c.Target + "\x1f" + c.Session
	if c.Global == shared.StateBlocked {
		if g.blocked[key] {
			return false
		}
		g.blocked[key] = true
		return true
	}
	delete(g.blocked, key)
	return false
}

// RunPushDispatcher subscribes to the broadcaster and drives a Web-Push (Tier 3)
// for every transition into the blocked state, gated by server-side presence
// de-dup. It drains the broadcaster channel promptly and runs each qualifying
// dispatch in its own goroutine so a slow push send can never stall the
// broadcaster fan-out. Only fresh blocked transitions spawn a goroutine (no
// throwaway goroutine per non-blocked change). It returns when ctx is cancelled
// or the subscription channel is closed.
func RunPushDispatcher(ctx context.Context, d DispatcherDeps) {
	_, ch, cancel := d.Bcast.Subscribe()
	defer cancel()
	gate := newBlockedGate()
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-ch:
			if !ok {
				return
			}
			if gate.fresh(c) {
				go dispatch(ctx, d, c)
			}
		}
	}
}

// dispatch sends a Web-Push for a single blocked Change. Non-blocked changes are
// ignored. For each principal that owns subscriptions and has no live SSE
// connection (Presence.Online == false), it pushes to every stored
// subscription, pruning any that the push service reports as expired
// (404 Gone / 410 Not Found).
func dispatch(ctx context.Context, d DispatcherDeps, c Change) {
	if c.Global != shared.StateBlocked {
		return
	}
	ids, err := d.Store.PrincipalIDsWithSubscriptions(ctx)
	if err != nil {
		return
	}
	ts := ""
	if d.NowRFC3339 != nil {
		ts = d.NowRFC3339()
	}
	payload, err := json.Marshal(pushMsg{
		Type:    "blocked",
		Server:  c.ServerID,
		Target:  c.Target,
		Session: c.Session,
		Ts:      ts,
	})
	if err != nil {
		return
	}
	for _, id := range ids {
		if d.Presence != nil && d.Presence.Online(id) {
			continue // server-side de-dup: a live page handles Tier 1/2.
		}
		subs, err := d.Store.ListSubscriptionsForPrincipal(ctx, id)
		if err != nil {
			continue
		}
		for _, s := range subs {
			status, err := d.Send(ctx, s, payload)
			if err == nil && (status == http.StatusNotFound || status == http.StatusGone) {
				_ = d.Store.DeleteSubscription(ctx, s.Endpoint) // prune expired subscription.
			}
		}
	}
}

// NewWebPushSender returns the production PushSender backed by webpush-go. It
// signs each request with the persisted VAPID keypair and the configured
// subject (a mailto:/URL contact required by the protocol).
func NewWebPushSender(keys db.VAPIDKeys, subject string) PushSender {
	// Bound every send: webpush-go otherwise uses an http.Client with NO timeout,
	// so a black-hole/slow push endpoint would pin the dispatch goroutine + socket
	// until hub shutdown (the dispatch ctx is the long-lived process ctx).
	client := &http.Client{Timeout: 10 * time.Second}
	return func(ctx context.Context, sub db.PushSubscription, payload []byte) (int, error) {
		resp, err := webpush.SendNotificationWithContext(ctx, payload, &webpush.Subscription{
			Endpoint: sub.Endpoint,
			Keys:     webpush.Keys{P256dh: sub.P256dh, Auth: sub.Auth},
		}, &webpush.Options{
			HTTPClient:      client,
			Subscriber:      subject,
			TTL:             60,
			VAPIDPublicKey:  keys.Public,
			VAPIDPrivateKey: keys.Private,
		})
		if err != nil {
			return 0, err
		}
		defer resp.Body.Close()
		return resp.StatusCode, nil
	}
}
