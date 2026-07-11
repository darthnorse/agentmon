// Package report implements the agent's orchestrator-report path: a loopback
// POST buffers runner stage reports; the hub drains them over its poll channel
// with an ack-on-next-drain cursor protocol (design doc §3–§4). The buffer is
// in-memory by design (D7): an agent restart loses at most a poll interval of
// reports, and GitHub reconcile covers the gap.
package report

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"sync"

	"agentmon/shared"
)

// DefaultCap bounds the buffer (mirrors the hub's maxPendingReports).
const DefaultCap = 256

type entry struct {
	seq    uint64
	target string
	r      shared.OrchestratorReport
}

// Store buffers reports until the hub acknowledges receipt. Drain(target,
// instance, ack) first deletes that target's entries with seq <= ack IF
// instance matches this store's lifetime id (a stale instance from before an
// agent restart must never delete fresh reports — D14), then returns every
// remaining entry for the target. At-least-once: a batch the hub never acks
// is simply redelivered; the hub's guarded transitions reject duplicates.
type Store struct {
	mu       sync.Mutex
	instance string
	max      int
	nextSeq  uint64
	entries  []entry
}

func NewStore(instance string, max int) *Store {
	if max <= 0 {
		max = DefaultCap
	}
	return &Store{instance: instance, max: max, nextSeq: 1}
}

// NewInstanceID mints the random 16-hex-char store-lifetime identifier.
// crypto/rand.Read never fails on supported platforms (Go ≥1.24 guarantee).
func NewInstanceID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Add buffers one report for target. On overflow the OLDEST entry is dropped:
// the newest report carries the freshest stage, and intermediate history is
// recoverable from GitHub state (D7).
func (s *Store) Add(target string, r shared.OrchestratorReport) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries = append(s.entries, entry{seq: s.nextSeq, target: target, r: r})
	s.nextSeq++
	if len(s.entries) > s.max {
		d := s.entries[0]
		s.entries = s.entries[1:]
		log.Printf("report: buffer full — dropped oldest (target=%q epic=%d stage=%s seq=%d)", d.target, d.r.Epic, d.r.Stage, d.seq)
	}
}

// Drain implements the ack-on-next-drain protocol for one target.
func (s *Store) Drain(target, instance string, ack uint64) shared.OrchestratorReportBatch {
	s.mu.Lock()
	defer s.mu.Unlock()
	if instance == s.instance && ack > 0 {
		kept := s.entries[:0]
		for _, e := range s.entries {
			if e.target == target && e.seq <= ack {
				continue
			}
			kept = append(kept, e)
		}
		s.entries = kept
	}
	batch := shared.OrchestratorReportBatch{Instance: s.instance, Reports: []shared.OrchestratorReport{}}
	for _, e := range s.entries {
		if e.target != target {
			continue
		}
		batch.Reports = append(batch.Reports, e.r)
		if e.seq > batch.Cursor {
			batch.Cursor = e.seq
		}
	}
	return batch
}
