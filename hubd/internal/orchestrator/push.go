package orchestrator

import (
	"context"
	"encoding/json"
	"log"

	"agentmon/hubd/internal/state"
	"agentmon/shared"
)

type BoardPushDeps struct {
	Bcast    *BoardBroadcaster
	Presence *state.Presence
	Store    state.PushDispatchStore
	Send     state.PushSender
	Now      func() string
}

// RunBoardPushDispatcher pushes on escalated/stalled board changes. Transitions
// are edge-triggered at the publisher, so no per-episode gate is needed here.
func RunBoardPushDispatcher(ctx context.Context, d BoardPushDeps) {
	_, ch, cancel := d.Bcast.Subscribe()
	defer cancel()
	for {
		select {
		case <-ctx.Done():
			return
		case c, ok := <-ch:
			if !ok {
				return
			}
			if c.Stage != shared.EpicEscalated && c.Stage != shared.EpicStalled {
				continue
			}
			go dispatchBoardPush(ctx, d, c)
		}
	}
}

func dispatchBoardPush(ctx context.Context, d BoardPushDeps, c BoardChange) {
	payload, err := json.Marshal(map[string]any{
		"type": "epic", "stage": string(c.Stage), "project": c.ProjectID,
		"epic": c.Issue, "title": c.Title, "needs": c.Needs, "ts": d.Now(),
	})
	if err != nil {
		return
	}
	ids, err := d.Store.PrincipalIDsWithSubscriptions(ctx)
	if err != nil {
		log.Printf("board push: principals: %v", err)
		return
	}
	for _, id := range ids {
		if d.Presence != nil && d.Presence.Online(id) {
			continue
		}
		subs, err := d.Store.ListSubscriptionsForPrincipal(ctx, id)
		if err != nil {
			continue
		}
		for _, sub := range subs {
			status, err := d.Send(ctx, sub, payload)
			if err != nil {
				log.Printf("board push: send: %v", err)
				continue
			}
			if status == 404 || status == 410 {
				_ = d.Store.DeleteSubscription(ctx, sub.Endpoint)
			}
		}
	}
}
