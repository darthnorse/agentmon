package agentbin

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestBinaryAndChecksumMatch(t *testing.T) {
	for _, arch := range []string{"amd64", "arm64"} {
		b, ok := Binary(arch)
		if !ok || len(b) == 0 {
			t.Fatalf("%s: no embedded bytes", arch)
		}
		want := sha256.Sum256(b)
		got, ok := SHA256Hex(arch)
		if !ok || got != hex.EncodeToString(want[:]) {
			t.Fatalf("%s: checksum mismatch got=%s", arch, got)
		}
	}
	if _, ok := Binary("sparc"); ok {
		t.Fatal("unknown arch must not resolve")
	}
}
