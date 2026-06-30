package state

import (
	"sync"
	"testing"
)

// TestPresence_AddRemoveOnline exercises the basic ref-counted online semantics:
// an unknown id is offline; a single Add makes it online; nested Add/Remove keeps
// it online until the count reaches zero; Remove below zero is a no-op (no panic,
// no negative count).
func TestPresence_AddRemoveOnline(t *testing.T) {
	p := NewPresence()

	if p.Online("x") {
		t.Fatalf("unknown id must be offline")
	}

	p.Add("u")
	if !p.Online("u") {
		t.Fatalf("after Add, id must be online")
	}

	// Nested connection: second Add then a single Remove keeps it online.
	p.Add("u")
	p.Remove("u")
	if !p.Online("u") {
		t.Fatalf("with two Adds and one Remove, id must still be online")
	}

	// Final Remove drops to zero → offline.
	p.Remove("u")
	if p.Online("u") {
		t.Fatalf("after balanced Remove, id must be offline")
	}

	// Remove below zero must never panic nor make the id online.
	p.Remove("u")
	p.Remove("u")
	if p.Online("u") {
		t.Fatalf("Remove below zero must keep id offline")
	}
}

// TestPresence_IsolatedIDs asserts that counts for different ids do not interfere.
func TestPresence_IsolatedIDs(t *testing.T) {
	p := NewPresence()
	p.Add("a")
	if p.Online("b") {
		t.Fatalf("Add(a) must not make b online")
	}
	p.Remove("a")
	if p.Online("a") {
		t.Fatalf("a must be offline after Remove")
	}
}

// TestPresence_ConcurrentAddRemove runs balanced Add/Remove from many goroutines
// on the same id and asserts the id ends offline, with -race detecting any
// unsynchronised map/counter access.
func TestPresence_ConcurrentAddRemove(t *testing.T) {
	p := NewPresence()
	const n = 100

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			p.Add("u")
		}()
	}
	wg.Wait()

	if !p.Online("u") {
		t.Fatalf("after %d concurrent Adds, id must be online", n)
	}

	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			p.Remove("u")
		}()
	}
	wg.Wait()

	if p.Online("u") {
		t.Fatalf("after %d balanced Removes, id must be offline", n)
	}
}

// TestPresence_ConcurrentMixedOnline hammers Add/Remove/Online concurrently on
// distinct ids to surface races under -race; correctness of the final count is
// not asserted here (the balanced test covers that), only data-race freedom.
func TestPresence_ConcurrentMixedOnline(t *testing.T) {
	p := NewPresence()
	const n = 200

	var wg sync.WaitGroup
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			id := "u"
			p.Add(id)
			_ = p.Online(id)
			p.Remove(id)
		}(i)
	}
	wg.Wait()

	if p.Online("u") {
		t.Fatalf("balanced concurrent churn must end offline")
	}
}
