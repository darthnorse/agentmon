// Package agentbin embeds the cross-compiled agent binaries the hub serves to the
// installer. In CI/unit builds these are tiny placeholders; the Docker image build
// overwrites them with the real static binaries before compiling hubd, so the
// served bytes and their advertised sha256 always match what the image ships.
package agentbin

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
)

//go:embed bin/agent-linux-amd64 bin/agent-linux-arm64
var files embed.FS

var paths = map[string]string{
	"amd64": "bin/agent-linux-amd64",
	"arm64": "bin/agent-linux-arm64",
}

var sums = func() map[string]string {
	m := map[string]string{}
	for arch, p := range paths {
		b, err := files.ReadFile(p)
		if err != nil {
			panic(err)
		}
		h := sha256.Sum256(b)
		m[arch] = hex.EncodeToString(h[:])
	}
	return m
}()

func Binary(arch string) ([]byte, bool) {
	p, ok := paths[arch]
	if !ok {
		return nil, false
	}
	b, err := files.ReadFile(p)
	if err != nil {
		return nil, false
	}
	return b, true
}

func SHA256Hex(arch string) (string, bool) {
	s, ok := sums[arch]
	return s, ok
}
