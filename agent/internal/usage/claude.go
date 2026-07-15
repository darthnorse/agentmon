package usage

import (
	"bufio"
	"encoding/json"
	"io"
)

type claudeLine struct {
	Type    string `json:"type"`
	Message struct {
		ID    string `json:"id"`
		Model string `json:"model"`
		Usage *struct {
			Input      int64 `json:"input_tokens"`
			Output     int64 `json:"output_tokens"`
			CacheRead  int64 `json:"cache_read_input_tokens"`
			CacheWrite int64 `json:"cache_creation_input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

// ParseClaude returns one MsgUsage per usage-bearing row (dedup happens globally
// in Aggregate). Malformed lines are skipped — capture is best-effort.
func ParseClaude(r io.Reader) ([]MsgUsage, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // transcript rows can be large
	var out []MsgUsage
	for sc.Scan() {
		var l claudeLine
		if json.Unmarshal(sc.Bytes(), &l) != nil || l.Message.Usage == nil {
			continue
		}
		u := l.Message.Usage
		out = append(out, MsgUsage{ID: l.Message.ID, Provider: "claude", Model: l.Message.Model,
			Input: u.Input, Output: u.Output, CacheRead: u.CacheRead, CacheWrite: u.CacheWrite})
	}
	return out, sc.Err()
}
