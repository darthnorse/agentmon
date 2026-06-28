package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"strings"
	"text/tabwriter"

	"agentmon/hubd/internal/audit"
	"agentmon/hubd/internal/config"
	"agentmon/hubd/internal/db"
)

// serverCmdStore is the DB surface the CLI needs (satisfied by *db.DB).
type serverCmdStore interface {
	FindServer(ctx context.Context, idOrHostname string) (db.Server, error)
	ListServers(ctx context.Context, status string) ([]db.Server, error)
	SetServerStatus(ctx context.Context, id, status string) (bool, error)
	DeleteServer(ctx context.Context, id string) (bool, error)
}

// serverAuditor is the audit surface (satisfied by *audit.Recorder).
type serverAuditor interface {
	ServerApprove(ctx context.Context, id, hostname string)
	ServerRevoke(ctx context.Context, id, hostname string)
	ServerRemove(ctx context.Context, id, hostname string)
}

// serverAction approves/revokes/removes a server, resolving id-or-hostname, and
// audits. Returns a human message.
func serverAction(ctx context.Context, d serverCmdStore, rec serverAuditor, action, idOrHostname string) (string, error) {
	srv, err := d.FindServer(ctx, idOrHostname)
	if errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("no server matching %q", idOrHostname)
	}
	if err != nil {
		return "", err
	}
	switch action {
	case "approve":
		if _, err := d.SetServerStatus(ctx, srv.ID, "active"); err != nil {
			return "", err
		}
		rec.ServerApprove(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("approved %s — now active and dialable", srv.ID), nil
	case "revoke":
		if _, err := d.SetServerStatus(ctx, srv.ID, "revoked"); err != nil {
			return "", err
		}
		rec.ServerRevoke(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("revoked %s — hub will stop dialing it", srv.ID), nil
	case "rm":
		if _, err := d.DeleteServer(ctx, srv.ID); err != nil {
			return "", err
		}
		rec.ServerRemove(ctx, srv.ID, srv.Hostname)
		return fmt.Sprintf("removed %s", srv.ID), nil
	default:
		return "", fmt.Errorf("unknown action %q", action)
	}
}

// serverList renders the full server table.
func serverList(ctx context.Context, d serverCmdStore) (string, error) {
	servers, err := d.ListServers(ctx, "")
	if err != nil {
		return "", err
	}
	var sb strings.Builder
	tw := tabwriter.NewWriter(&sb, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tNAME\tHOSTNAME\tURL\tSTATUS\tOS/ARCH\tVERSION\tLAST-SEEN")
	for _, s := range servers {
		last := s.LastSeenAt
		if last == "" {
			last = "never"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s/%s\t%s\t%s\n",
			s.ID, s.Name, s.Hostname, s.URL, s.Status, s.OS, s.Arch, s.AgentVersion, last)
	}
	tw.Flush()
	return sb.String(), nil
}

// runServerCmd implements: agentmon-hubd server list|approve|revoke|rm [<id|hostname>] [--config <path>]
func runServerCmd(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: agentmon-hubd server <list|approve|revoke|rm> [<id|hostname>]")
	}
	sub := args[0]
	fs := flag.NewFlagSet("server", flag.ExitOnError)
	cfgPath := fs.String("config", "/data/config.yaml", "path to config.yaml")
	fs.Parse(args[1:]) //nolint:errcheck // ExitOnError never returns

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	database, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer database.Close()
	ctx := context.Background()
	rec := audit.NewRecorder(database)

	if sub == "list" {
		out, err := serverList(ctx, database)
		if err != nil {
			return err
		}
		fmt.Print(out)
		return nil
	}
	rest := fs.Args()
	if len(rest) < 1 {
		return fmt.Errorf("usage: agentmon-hubd server %s <id|hostname>", sub)
	}
	switch sub {
	case "approve", "revoke", "rm":
		msg, err := serverAction(ctx, database, rec, sub, rest[0])
		if err != nil {
			return err
		}
		fmt.Println(msg)
		return nil
	default:
		return fmt.Errorf("unknown subcommand %q (want list|approve|revoke|rm)", sub)
	}
}
