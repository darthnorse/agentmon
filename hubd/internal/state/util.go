package state

import "time"

// hubTS formats a hub-clock timestamp for storage/comparison. Fixed width so
// lexical string comparison is chronological. Single clock for the seen invariant.
func hubTS(t time.Time) string { return t.UTC().Format("2006-01-02 15:04:05.000") }
