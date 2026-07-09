package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"

	"agentmon/agent/internal/config"
	"agentmon/agent/internal/hooks"
)

// printWarnings writes any InstallWarnings for cfg to stderr.
func printWarnings(cfg config.Config) {
	for _, w := range hooks.InstallWarnings(cfg) {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
}

// hooksMain runs `agentmon-agent hooks <print|install|uninstall>`.
func hooksMain(args []string, stdout io.Writer) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentmon-agent hooks <print|install|uninstall> [--provider claude|codex] [--config p] [--settings p]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("hooks "+sub, flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	providerName := fs.String("provider", string(hooks.ProviderClaude), "hook provider for print/install: claude or codex (uninstall removes all AgentMon-marked hooks)")
	settings := fs.String("settings", "", "path to the provider hook settings JSON (required for install/uninstall)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	provider, err := hooks.ParseProvider(*providerName)
	if err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	switch sub {
	case "print":
		printWarnings(cfg)
		snip, err := hooks.Snippet(cfg, provider)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snip)
	case "install":
		printWarnings(cfg)
		if *settings == "" {
			return fmt.Errorf("hooks install requires --settings <PATH>")
		}
		existing, err := hooks.LoadSettings(*settings)
		if err != nil {
			return err
		}
		merged, err := hooks.Merge(existing, cfg, provider)
		if err != nil {
			return err
		}
		if err := hooks.SaveSettings(*settings, merged); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "installed AgentMon %s hooks into %s\n", provider, *settings)
		return nil
	case "uninstall":
		if *settings == "" {
			return fmt.Errorf("hooks uninstall requires --settings <PATH>")
		}
		existing, err := hooks.LoadSettings(*settings)
		if err != nil {
			return err
		}
		if err := hooks.SaveSettings(*settings, hooks.Unmerge(existing)); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "removed AgentMon hooks from %s\n", *settings)
		return nil
	default:
		return fmt.Errorf("unknown hooks subcommand %q", sub)
	}
}

// hookTestMain runs `agentmon-agent hook-test` — synthesizes a hook POST to the
// local agent to verify the wiring end-to-end.
func hookTestMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("hook-test", flag.ContinueOnError)
	fs.SetOutput(stdout)
	cfgPath := fs.String("config", "/etc/agentmon/agent.toml", "path to agent.toml")
	pane := fs.String("pane", os.Getenv("TMUX_PANE"), "tmux pane id (defaults to $TMUX_PANE)")
	event := fs.String("event", "Stop", "hook event name")
	kind := fs.String("notification-kind", "", "notification_type (for Notification)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("config: %w", err)
	}
	if cfg.HookToken == "" {
		return fmt.Errorf("hook_token not configured; /hook is disabled")
	}
	_, port, err := net.SplitHostPort(cfg.Listen)
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	body := fmt.Sprintf(`{"hook_event_name":%q,"notification_type":%q,"session_id":"hook-test"}`, *event, *kind)
	req, err := http.NewRequest(http.MethodPost, "http://127.0.0.1:"+port+"/hook", strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HookToken)
	req.Header.Set("X-AgentMon-Pane", *pane)
	req.Header.Set("X-AgentMon-Tmux", os.Getenv("TMUX"))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("post: %w", err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, resp.Body)
	fmt.Fprintf(stdout, "hook-test → HTTP %d\n", resp.StatusCode)
	return nil
}
