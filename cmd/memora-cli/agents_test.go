package main

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	mcli "github.com/axiom-studio/memora/internal/cli"
	"github.com/axiom-studio/memora/internal/testmesh"
	"github.com/axiom-studio/memora/pkg/client"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestCLI_AgentsRegister (#6175): `agents register --agent-id <uuid>`
// registers that agent, wherever the flag sits on the command line, and
// without the flag it registers the agent the CLI writes as. The
// command line goes through parseGlobal, as in main.
func TestCLI_AgentsRegister(t *testing.T) {
	n := testmesh.New(t, []string{"a"}, testmesh.Options{})[0]
	_, ws := n.Do(t, "POST", "/v1/workspaces", map[string]any{"name": "agents"}, nil)
	wsID := ws["id"].(string)
	register := func(argv ...string) types.Agent {
		t.Helper()
		g := parseGlobal(argv)
		var buf bytes.Buffer
		g.out = mcli.NewOutput(mcli.ParseFormat("json"), true, false, false)
		g.out.W = &buf
		g.Endpoint, g.Output, g.Workspace, g.Args = n.TS.URL, "json", wsID, g.Args[1:] // drop "agents"
		cmdAgents(context.Background(), client.New(n.TS.URL, ""), g)
		var a types.Agent
		if err := json.Unmarshal(buf.Bytes(), &a); err != nil {
			t.Fatalf("%v: output %q: %v", argv, buf.String(), err)
		}
		return a
	}
	first, second := types.NewID(), types.NewID()
	if a := register("agents", "register", "--agent-id", first); a.AgentID != first {
		t.Fatalf("registered %q, want %s", a.AgentID, first)
	}
	if a := register("--agent-id", second, "agents", "register", "--display-name", "second"); a.AgentID != second || a.DisplayName != "second" {
		t.Fatalf("registered %+v, want %s named second", a, second)
	}
	t.Setenv("MEMORA_AGENT_ID", "")
	if a := register("agents", "register"); a.AgentID != defaultAgentID {
		t.Fatalf("without --agent-id registered %q, want the CLI's own agent %s", a.AgentID, defaultAgentID)
	}

	_, list := n.Do(t, "GET", "/v1/workspaces/"+wsID+"/agents", nil, nil)
	seen := map[string]bool{}
	for _, a := range list["agents"].([]any) {
		seen[a.(map[string]any)["agent_id"].(string)] = true
	}
	for _, id := range []string{first, second, defaultAgentID} {
		if !seen[id] {
			t.Errorf("agents list lacks %s: %v", id, list)
		}
	}
}
