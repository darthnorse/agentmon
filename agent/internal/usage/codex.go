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
func ParseCodex(r io.Reader) (MsgUsage, bool, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var last MsgUsage
	var found bool
	for sc.Scan() {
		var l codexLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Payload.Type != "token_count" {
			continue
		}
		t := l.Payload.Info.Total
		last = MsgUsage{Provider: "codex", Model: l.Payload.Model,
			Input: t.Input - t.Cached, Output: t.Output, CacheRead: t.Cached, CacheWrite: 0}
		found = true
	}
	return last, found, sc.Err()
}
