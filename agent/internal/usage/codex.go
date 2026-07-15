package usage

import (
	"bufio"
	"encoding/json"
	"io"
)

type codexLine struct {
	Payload struct {
		Type  string `json:"type"`
		Model string `json:"model"`
		Info  struct {
			Total struct {
				Input  int64 `json:"input_tokens"`
				Cached int64 `json:"cached_input_tokens"`
				Output int64 `json:"output_tokens"`
			} `json:"total_token_usage"`
		} `json:"info"`
	} `json:"payload"`
}

// ParseCodex returns the LAST token_count record's cumulative total, normalized:
// input_tokens INCLUDES cached, so fresh input = input-cached, cache_read=cached.
//
// The model is NOT reliably present on token_count records (live rollouts:
// token_count's payload.info carries totals but no payload.model). It shows
// up on other record kinds instead (e.g. turn_context's payload.model,
// collaboration_mode's nested payload.collaboration_mode.settings.model), so
// this tracks the most-recent non-empty payload.model seen across ANY record
// in the rollout and pairs it with the last token_count total. A rollout is
// one session/model, so last-seen model == the session's model. If no record
// ever carries a model, Model stays "" — best-effort, never fails closed on
// the totals.
func ParseCodex(r io.Reader) (MsgUsage, bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var last MsgUsage
	var found bool
	var model string
	for sc.Scan() {
		var l codexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil {
			continue
		}
		if l.Payload.Model != "" {
			model = l.Payload.Model
		}
		if l.Payload.Type != "token_count" {
			continue
		}
		t := l.Payload.Info.Total
		last = MsgUsage{Provider: "codex",
			Input: t.Input - t.Cached, Output: t.Output, CacheRead: t.Cached, CacheWrite: 0}
		found = true
	}
	last.Model = model
	return last, found, sc.Err()
}
