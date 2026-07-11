package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"agentmon/agent/internal/runnerfiles"
)

// installSkillsMain runs `agentmon install-skills [--home DIR]` — writes the
// embedded runner skills into the user's provider dirs. The installer invokes
// it via runuser with an explicit --home so the destination never depends on
// runuser's environment handling.
func installSkillsMain(args []string, stdout io.Writer) error {
	fs := flag.NewFlagSet("install-skills", flag.ContinueOnError)
	fs.SetOutput(stdout)
	home := fs.String("home", "", "home directory to install into (default: current user's)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	h := *home
	if h == "" {
		var err error
		if h, err = os.UserHomeDir(); err != nil {
			return err
		}
	}
	written, err := runnerfiles.InstallSkills(h)
	for _, p := range written {
		fmt.Fprintf(stdout, "installed %s\n", p)
	}
	return err
}
