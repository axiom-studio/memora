package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	mcli "github.com/axiom-studio/memora/internal/cli"
	"github.com/axiom-studio/memora/pkg/client"
)

// recordedRequest is what a subcommand sent to the server.
type recordedRequest struct {
	path, query string
	body        map[string]any
}

// recorder is a real HTTP server that records each request and answers
// with an empty JSON object, which every client decoder accepts.
func recorder(t *testing.T) (*httptest.Server, func() recordedRequest) {
	t.Helper()
	var mu sync.Mutex
	var last recordedRequest
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var body map[string]any
		_ = json.Unmarshal(b, &body)
		mu.Lock()
		last = recordedRequest{path: r.URL.Path, query: r.URL.RawQuery, body: body}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	t.Cleanup(ts.Close)
	return ts, func() recordedRequest { mu.Lock(); defer mu.Unlock(); return last }
}

// TestPositionalCommands_HonourFlagsBeforeAndAfter (#6161): every
// subcommand with positional arguments honours its flags wherever they
// sit, and never folds them into the positionals.
func TestPositionalCommands_HonourFlagsBeforeAndAfter(t *testing.T) {
	const ws, mem, mem2 = "0e7d4d8a-5f1c-4b8e-9a3d-1c2b3a4d5e6f", "6a1f0c2e-3b4d-4e5f-8a6b-7c8d9e0f1a2b", "9b8a7c6d-5e4f-4a3b-b2c1-d0e9f8a7b6c5"
	type check func(t *testing.T, r recordedRequest)
	bodyHas := func(want map[string]any) check {
		return func(t *testing.T, r recordedRequest) {
			for k, v := range want {
				if got, _ := json.Marshal(r.body[k]); string(got) != mustJSON(v) {
					t.Errorf("body[%s] = %s, want %s (body %v)", k, got, mustJSON(v), r.body)
				}
			}
		}
	}
	pathIs := func(p string) check {
		return func(t *testing.T, r recordedRequest) {
			if r.path != p {
				t.Errorf("path = %s, want %s", r.path, p)
			}
		}
	}
	cases := []struct {
		name   string
		run    func(context.Context, *client.Client, globalFlags)
		args   [][]string // equivalent argument orders
		checks []check
	}{
		{"search", cmdSearch, [][]string{
			{"--mode", "keyword", "--k", "1", "--workspaces", ws, "NVDA", "chips"},
			{"NVDA", "chips", "--mode", "keyword", "--k", "1", "--workspaces", ws},
			{"NVDA", "--k", "1", "chips", "--workspaces", ws, "--mode", "keyword"},
		}, []check{pathIs("/v1/search"), bodyHas(map[string]any{"query": "NVDA chips", "mode": "keyword", "k": 1, "workspace_ids": []string{ws}})}},
		{"recall", cmdRecall, [][]string{
			{"--mode", "keyword", "--k", "1", "NVDA"},
			{"NVDA", "--mode", "keyword", "--k", "1"},
		}, []check{pathIs("/v1/workspaces/" + ws + "/recall"), bodyHas(map[string]any{"query": "NVDA", "mode": "keyword", "k": 1})}},
		{"update", cmdUpdate, [][]string{
			{"--text", "new body", "--if-match", "w1", mem},
			{mem, "--text", "new body", "--if-match", "w1"},
		}, []check{pathIs("/v1/workspaces/" + ws + "/memories/" + mem), bodyHas(map[string]any{"content": "new body", "expected_watermark": "w1"})}},
		{"append", cmdAppend, [][]string{
			{"--text", "more", mem},
			{mem, "--text", "more"},
		}, []check{pathIs("/v1/workspaces/" + ws + "/memories/" + mem + ":append"), bodyHas(map[string]any{"content": "more"})}},
		{"patch", cmdPatch, [][]string{
			{"--patch", `[{"old_string":"a","new_string":"b"}]`, mem},
			{mem, "--patch", `[{"old_string":"a","new_string":"b"}]`},
		}, []check{pathIs("/v1/workspaces/" + ws + "/memories/" + mem)}},
		{"link", cmdLink, [][]string{
			{"--type", "supersedes", mem, mem2},
			{mem, mem2, "--type", "supersedes"},
			{mem, "--type", "supersedes", mem2},
		}, []check{pathIs("/v1/workspaces/" + ws + "/memories/" + mem + "/edges"), bodyHas(map[string]any{"target_memory_id": mem2, "edge_type": "supersedes"})}},
		{"neighbors", cmdNeighbors, [][]string{
			{"--direction", "in", "--k", "3", mem},
			{mem, "--direction", "in", "--k", "3"},
		}, []check{pathIs("/v1/workspaces/" + ws + "/graph/neighbors"), bodyHas(map[string]any{"memory_id": mem, "direction": "in", "k": 3})}},
		{"traverse", cmdTraverse, [][]string{
			{"--depth", "2", "--direction", "both", mem},
			{mem, "--depth", "2", "--direction", "both"},
		}, []check{pathIs("/v1/workspaces/" + ws + "/graph/traverse"), bodyHas(map[string]any{"seed_memory_id": mem, "depth": 2, "direction": "both"})}},
	}
	for _, tc := range cases {
		for _, args := range tc.args {
			t.Run(tc.name+" "+strings.Join(args, " "), func(t *testing.T) {
				ts, last := recorder(t)
				g := globalFlags{Endpoint: ts.URL, Workspace: ws, Output: "json", Args: args}
				g.out = mcli.NewOutput(mcli.ParseFormat("json"), true, true, false)
				c := client.New(ts.URL, "")
				c.Workspace = ws
				tc.run(context.Background(), c, g)
				for _, chk := range tc.checks {
					chk(t, last())
				}
			})
		}
	}
}

func mustJSON(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
