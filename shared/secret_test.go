package shared

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveSecretRef(t *testing.T) {
	t.Setenv("AGENTMON_T_SECRET", "s3cr3t")
	f := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(f, []byte("  filetok\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := ResolveSecretRef("env:AGENTMON_T_SECRET"); err != nil || got != "s3cr3t" {
		t.Fatalf("env: got %q err %v", got, err)
	}
	if got, err := ResolveSecretRef("file:" + f); err != nil || got != "filetok" {
		t.Fatalf("file: got %q err %v", got, err)
	}
	if _, err := ResolveSecretRef(""); err == nil {
		t.Fatal("empty ref must error")
	}
	if _, err := ResolveSecretRef("env:DEFINITELY_UNSET_AGENTMON"); err == nil {
		t.Fatal("unset env must error")
	}
}

func TestResolveSecretRefRejectsBareLiteralWithoutEchoingIt(t *testing.T) {
	_, err := ResolveSecretRef("sk-this-is-a-literal-secret")
	if err == nil {
		t.Fatal("bare literal must be rejected")
	}
	if strings.Contains(err.Error(), "sk-this-is-a-literal-secret") {
		t.Fatalf("error must NOT echo the secret value: %v", err)
	}
}
