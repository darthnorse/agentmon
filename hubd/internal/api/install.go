package api

import (
	_ "embed"
	"net/http"
	"strconv"
	"strings"
	"text/template"

	"agentmon/hubd/internal/agentbin"
)

//go:embed install.sh.tmpl
var installScriptTmpl string

var installTmpl = template.Must(template.New("install").Parse(installScriptTmpl))

type InstallDeps struct {
	HubURL string
}

type installData struct {
	HubURL      string
	SHA256AMD64 string
	SHA256ARM64 string
}

func (d InstallDeps) ScriptHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		amd, _ := agentbin.SHA256Hex("amd64")
		arm, _ := agentbin.SHA256Hex("arm64")
		w.Header().Set("Content-Type", "text/x-shellscript; charset=utf-8")
		if err := installTmpl.Execute(w, installData{HubURL: strings.TrimRight(d.HubURL, "/"), SHA256AMD64: amd, SHA256ARM64: arm}); err != nil {
			// Header already sent on partial write; nothing more to do but log upstream.
			return
		}
	}
}

func (d InstallDeps) BinaryHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		file := r.PathValue("file")
		arch := strings.TrimPrefix(file, "agent-linux-")
		if arch == file { // prefix not present
			http.NotFound(w, r)
			return
		}
		b, ok := agentbin.Binary(arch)
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Content-Length", strconv.Itoa(len(b)))
		w.Header().Set("Content-Disposition", `attachment; filename="agentmon-agent"`)
		_, _ = w.Write(b)
	}
}
