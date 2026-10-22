package http

import "testing"

// TestIsWriteVerb: only REST memory/edge/graph/pin writes need the agent
// header; console, peer, admin and query routes do not (#6183).
func TestIsWriteVerb(t *testing.T) {
	ws := "/v1/workspaces/2d6f1a3e-4b5c-4d7e-8f90-a1b2c3d4e5f6"
	mem := ws + "/memories/9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d"
	for _, c := range []struct {
		method, path string
		want         bool
	}{
		{"POST", ws + "/memories", true},
		{"PUT", mem, true},
		{"PATCH", mem, true},
		{"DELETE", mem, true},
		{"POST", mem + ":append", true},
		{"PUT", mem + "/tags/team", true},
		{"POST", mem + "/edges", true},
		{"POST", ws + "/edges/:batch", true},
		{"DELETE", ws + "/edges/1b2c3d4e-5f60-4a7b-8c9d-0e1f2a3b4c5d", true},
		{"POST", ws + "/graph/neighbors", true},
		{"POST", ws + "/recall/pins", true},
		{"DELETE", ws + "/recall/pins/3c4d5e6f-7a8b-4c9d-8e0f-1a2b3c4d5e6f", true},
		{"GET", mem, false},
		{"POST", ws + "/recall", false},
		{"POST", "/v1/search", false},
		{"POST", "/v1/workspaces", false},
		{"PUT", ws, false},
		{"POST", ws + "/collections", false},
		{"POST", ws + "/agents", false},
		{"POST", "/v1/federation/search", false},
		{"GET", "/v1/federation/memories/9a8b7c6d-5e4f-4a3b-8c2d-1e0f9a8b7c6d", false},
		{"POST", "/v1/federation/peers", false},
		{"POST", "/ui/api/memories/imprint", false},
		{"POST", "/ui/api/memories/forget", false},
		{"POST", "/ui/api/edges/link", false},
		{"POST", "/ui/api/edges/unlink", false},
		{"POST", "/ui/api/graph/traverse", false},
		{"POST", "/ui/api/workspaces/memories/edges", false},
	} {
		if got := isWriteVerb(c.method, c.path); got != c.want {
			t.Errorf("isWriteVerb(%s %s) = %v, want %v", c.method, c.path, got, c.want)
		}
	}
}
