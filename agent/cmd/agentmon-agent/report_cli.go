package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"agentmon/agent/internal/config"
	"agentmon/shared"
)

// reportMain runs `agentmon report --epic N --stage S [--note …] [--pr N]
// [--repo owner/name]` — the runner contract's stage-report verb (design doc
// §7). It POSTs to the local agent's loopback intake; the agent stamps the
// session name server-side, so no session flag exists here by design.
func reportMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("report", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	epic := fs.Int("epic", 0, "epic issue number (required)")
	stage := fs.String("stage", "", "planning|implementing|reviewing|pr_open|escalated (required)")
	note := fs.String("note", "", "optional note (escalation reason, checkpoint summary)")
	pr := fs.Int("pr", 0, "PR number (use with --stage pr_open)")
	repo := fs.String("repo", "", "owner/name (default: derived from the cwd's git remote origin)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *epic <= 0 {
		return fmt.Errorf("--epic is required (positive issue number)")
	}
	if !shared.ReportableStage(shared.EpicStage(*stage)) {
		return fmt.Errorf("--stage must be one of: planning, implementing, reviewing, pr_open, escalated")
	}
	r := *repo
	if r == "" {
		var err error
		if r, err = repoFromGit("."); err != nil {
			return err
		}
	}
	body, err := postReport(*cfgPath, map[string]any{
		"repo": r, "epic": *epic, "stage": *stage, "note": *note, "pr": *pr,
	}, false)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "reported epic %d stage %s (%s)\n", *epic, *stage, strings.TrimSpace(body))
	return nil
}

// postReport delivers one payload to the local intake (dryRun → ?dry_run=1,
// validate-only). Returns the response body. Shared with the doctor.
func postReport(cfgPath string, payload map[string]any, dryRun bool) (string, error) {
	pane := os.Getenv("TMUX_PANE")
	if pane == "" {
		return "", fmt.Errorf("agentmon report must run inside a tmux pane ($TMUX_PANE is empty)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return "", fmt.Errorf("config: %w", err)
	}
	if cfg.HookToken == "" {
		return "", fmt.Errorf("hook_token not configured; the report intake is disabled")
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return "", fmt.Errorf("listen: %w", err)
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}
	u := "http://127.0.0.1:" + port + "/orchestrator/report"
	if dryRun {
		u += "?dry_run=1"
	}
	req, err := http.NewRequest(http.MethodPost, u, bytes.NewReader(b))
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-AgentMon-Pane", pane)
	req.Header.Set("X-AgentMon-Tmux", os.Getenv("TMUX"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("post report: %w", err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("report rejected: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}
	return string(respBody), nil
}

// repoFromGit derives owner/name from dir's git remote origin.
func repoFromGit(dir string) (string, error) {
	out, err := exec.Command("git", "-C", dir, "config", "--get", "remote.origin.url").Output()
	if err != nil {
		return "", fmt.Errorf("cannot read git remote origin (pass --repo owner/name): %w", err)
	}
	return normalizeRepoURL(strings.TrimSpace(string(out)))
}

// normalizeRepoURL reduces a git remote URL to "owner/name". Handles
// git@host:owner/name(.git), https://host/owner/name(.git), ssh://git@host/owner/name.
func normalizeRepoURL(u string) (string, error) {
	s := strings.TrimSuffix(strings.TrimSpace(u), ".git")
	if i := strings.Index(s, "://"); i >= 0 { // URL form: strip scheme, then host
		s = s[i+3:]
		if j := strings.Index(s, "/"); j >= 0 {
			s = s[j+1:]
		}
	} else if i := strings.Index(s, ":"); i >= 0 { // scp-like git@host:owner/name
		s = s[i+1:]
	}
	parts := strings.Split(strings.Trim(s, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", fmt.Errorf("cannot derive owner/name from remote %q — pass --repo owner/name", u)
	}
	return parts[0] + "/" + parts[1], nil
}
