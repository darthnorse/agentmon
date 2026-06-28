// Package registry holds the config-driven server list and dials agents. The
// internal config.Server (URL + bearer token) is hub-side only; List/ServerSummary
// are the browser-safe projections (no secrets).
package registry

import "agentmon/hubd/internal/config"

type ServerSummary struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Labels  []string `json:"labels"`
	Enabled bool     `json:"enabled"`
}

type ServerDetail struct {
	ID      string   `json:"id"`
	Name    string   `json:"name"`
	Labels  []string `json:"labels"`
	Enabled bool     `json:"enabled"`
	Healthy bool     `json:"healthy"`
}

type Registry struct {
	order   []string
	servers map[string]config.Server
}

func New(servers []config.Server) *Registry {
	r := &Registry{servers: make(map[string]config.Server, len(servers))}
	for _, s := range servers {
		r.order = append(r.order, s.ID)
		r.servers[s.ID] = s
	}
	return r
}

func labelsOrEmpty(l []string) []string {
	if l == nil {
		return []string{}
	}
	return l
}

func (r *Registry) List() []ServerSummary {
	out := make([]ServerSummary, 0, len(r.order))
	for _, id := range r.order {
		s := r.servers[id]
		out = append(out, ServerSummary{ID: s.ID, Name: s.Name, Labels: labelsOrEmpty(s.Labels), Enabled: true})
	}
	return out
}

func (r *Registry) Get(id string) (config.Server, bool) {
	s, ok := r.servers[id]
	return s, ok
}
