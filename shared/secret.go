package shared

import (
	"fmt"
	"os"
	"strings"
)

// ResolveSecretRef expands a secret reference. It REQUIRES an explicit scheme so
// a typo or a pasted plaintext secret can never be silently accepted:
//
//	"env:NAME"  → value of environment variable NAME (error if unset)
//	"file:PATH" → trimmed contents of PATH (error wrapped)
//
// An empty ref or any other form is an error. The error never echoes v, since a
// bare-literal ref could itself be the secret.
func ResolveSecretRef(v string) (string, error) {
	switch {
	case v == "":
		return "", fmt.Errorf("empty secret ref")
	case strings.HasPrefix(v, "env:"):
		name := strings.TrimPrefix(v, "env:")
		s, ok := os.LookupEnv(name)
		if !ok {
			return "", fmt.Errorf("env ref %q not set", name)
		}
		return s, nil
	case strings.HasPrefix(v, "file:"):
		b, err := os.ReadFile(strings.TrimPrefix(v, "file:"))
		if err != nil {
			return "", fmt.Errorf("file ref: %w", err)
		}
		return strings.TrimSpace(string(b)), nil
	default:
		return "", fmt.Errorf("secret ref must use an env: or file: scheme")
	}
}
