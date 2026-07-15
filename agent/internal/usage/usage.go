package usage

// MsgUsage is one raw provider usage record. Buckets are disjoint (fresh input).
type MsgUsage struct {
	ID         string // message id (Claude) — dedup key; "" for Codex
	Provider   string
	Model      string
	Input      int64
	Output     int64
	CacheRead  int64
	CacheWrite int64
}
