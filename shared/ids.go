package shared

import "strings"

func ServerID(s string) string  { return "server:" + s }
func UserID(id string) string   { return "user:" + id }

func SessionID(server, target, name string) string {
	return "session:" + server + "/" + target + "/" + name
}

func PaneID(server, target, pane string) string {
	return "pane:" + server + "/" + target + "/" + pane
}

// ParsePaneID parses "pane:<server>/<target>/<paneId>". The paneId itself never
// contains '/', so we split the body into exactly 3 parts.
func ParsePaneID(rid string) (server, target, pane string, ok bool) {
	body, found := strings.CutPrefix(rid, "pane:")
	if !found {
		return "", "", "", false
	}
	parts := strings.SplitN(body, "/", 3)
	if len(parts) != 3 || parts[0] == "" || parts[1] == "" || parts[2] == "" {
		return "", "", "", false
	}
	return parts[0], parts[1], parts[2], true
}
