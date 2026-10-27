package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"

	mcli "github.com/axiom-studio/memora/internal/cli"
	"github.com/axiom-studio/memora/internal/testmesh"
	"github.com/axiom-studio/memora/pkg/client"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// run executes one CLI command against node with the given output
// format and returns what it printed. Text output goes to stdout, so it
// is captured through a pipe.
func run(t *testing.T, node *testmesh.Node, cmd func(context.Context, *client.Client, globalFlags), format string, args ...string) string {
	t.Helper()
	var buf bytes.Buffer
	out := mcli.NewOutput(mcli.ParseFormat(format), true, false, false)
	out.W = &buf
	g := globalFlags{Endpoint: node.TS.URL, Output: format, Args: args, out: out}
	if format != "text" {
		cmd(context.Background(), client.New(node.TS.URL, ""), g)
		return buf.String()
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	cmd(context.Background(), client.New(node.TS.URL, ""), g)
	os.Stdout = stdout
	_ = w.Close()
	b, _ := io.ReadAll(r)
	return string(b)
}

// TestCLI_FederatedSearchAndFetch (F-9): `search --federated` and
// `fetch --node` against node a of a real two-node mesh, in every output
// format; the JSON forms parse and name each hit's source.
func TestCLI_FederatedSearchAndFetch(t *testing.T) {
	nodes := testmesh.New(t, []string{"a", "b"}, testmesh.Options{})
	a, b := nodes[0], nodes[1]
	a.Seed(t, "glacier retreat photos")
	memB := b.Seed(t, "glacier mass balance table\n\nmore rows")

	searchArgs := []string{"glacier", "--mode", "keyword", "--federated", "--peers", b.ID, "--per-peer-k", "3", "--include-content"}
	var resp api.RecallResponse
	if err := json.Unmarshal([]byte(run(t, a, cmdSearch, "json", searchArgs...)), &resp); err != nil {
		t.Fatalf("search json: %v", err)
	}
	var fromB *api.RecallHit
	for i, h := range resp.Results {
		if h.MemoryID == memB {
			fromB = &resp.Results[i]
		}
	}
	if fromB == nil || fromB.NodeID != b.ID || fromB.NodeName != "b" || fromB.WorkspaceID == "" || fromB.MatchedText == "" || !strings.Contains(fromB.Content, "more rows") {
		t.Fatalf("search json hits = %+v, want b's memory with its source and content", resp.Results)
	}

	line := strings.TrimSpace(run(t, a, cmdSearch, "jsonl", searchArgs...))
	if strings.Count(line, "\n") != 0 || !json.Valid([]byte(line)) {
		t.Fatalf("search jsonl is not one JSON line: %q", line)
	}
	if y := run(t, a, cmdSearch, "yaml", searchArgs...); !strings.Contains(y, memB) || !strings.Contains(y, b.ID) {
		t.Fatalf("search yaml lacks b's hit: %s", y)
	}
	if txt := run(t, a, cmdSearch, "text", searchArgs...); !strings.Contains(txt, "node=b") || !strings.Contains(txt, memB) {
		t.Fatalf("search text = %q", txt)
	}

	var env api.MemoryEnvelope
	if err := json.Unmarshal([]byte(run(t, a, cmdFetch, "json", memB, "--node", b.ID)), &env); err != nil {
		t.Fatalf("fetch json: %v", err)
	}
	if env.Memory == nil || env.ID != memB || env.NodeID != b.ID || !strings.Contains(env.Content, "more rows") || len(env.Cells) == 0 {
		t.Fatalf("fetch json = %+v", env)
	}
	sc := bufio.NewScanner(strings.NewReader(run(t, a, cmdFetch, "jsonl", "--node", b.ID, memB)))
	if !sc.Scan() || !json.Valid(sc.Bytes()) || sc.Scan() {
		t.Fatal("fetch jsonl is not one JSON line")
	}
	if txt := run(t, a, cmdFetch, "text", memB, "--node", b.ID); !strings.Contains(txt, "node:      b ("+b.ID+")") || !strings.Contains(txt, "more rows") {
		t.Fatalf("fetch text = %q", txt)
	}
}
