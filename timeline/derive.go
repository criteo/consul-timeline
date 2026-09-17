package tl

import "strings"

// Derive tells the watcher how to read the team, app and version columns
// off a registration: which service meta keys fill each one, the first key
// present winning. The defaults cover common Kubernetes and Marathon
// registrations; a site with its own conventions sets them in the config
// file or with the -derive-* flags.
type Derive struct {
	Team    []string `json:"team"`
	App     []string `json:"app"`
	Version []string `json:"version"`
}

var DefaultDerive = Derive{
	Team:    []string{"team", "owner", "owners"},
	App:     []string{"app", "application"},
	Version: []string{"version"},
}

// Apply extracts team, app and version from a service's meta.
func (d Derive) Apply(meta map[string]string) (team, app, version string) {
	return first(meta, d.Team), first(meta, d.App), first(meta, d.Version)
}

// SplitList splits a comma separated flag value, dropping blanks.
func SplitList(s string) []string {
	var out []string
	for _, v := range strings.Split(s, ",") {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

func first(meta map[string]string, keys []string) string {
	for _, k := range keys {
		if v := meta[k]; v != "" {
			return v
		}
	}
	return ""
}
