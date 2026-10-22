package service

import (
	"context"
	"errors"
	"testing"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// TestDeactivatedAgent_EveryWritePath (#6169): each service write
// refuses an agent deactivated in that workspace, whatever the caller
// (REST, MCP, console, internal), and TouchAgent never reactivates it.
func TestDeactivatedAgent_EveryWritePath(t *testing.T) {
	ctx := context.Background()
	s := newTestService(t)
	ws := newTestWorkspace(t, s)
	bad := types.NewID()
	if err := s.RegisterAgent(ctx, &types.Agent{AgentID: bad, WorkspaceID: ws, DisplayName: "bad", IdentityProvider: "opaque"}, ""); err != nil {
		t.Fatal(err)
	}
	m1 := imprint(t, s, ws, "first")
	m2 := imprint(t, s, ws, "second")
	edge, err := s.GraphLink(ctx, types.Edge{WorkspaceID: ws, SourceMemoryID: m1, TargetMemoryID: m2, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: testAgent})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeactivateAgent(ctx, ws, bad, testAgent); err != nil {
		t.Fatal(err)
	}
	m, _ := s.Metadata.GetMemory(ctx, m1)
	wmk := m.HeadWatermark
	for name, write := range map[string]func() error{
		"imprint": func() error { _, err := s.Imprint(ctx, ws, bad, api.ImprintRequest{Content: "x"}); return err },
		"update":  func() error { _, err := s.Update(ctx, ws, m1, bad, wmk, api.UpdateRequest{Content: "x"}); return err },
		"patch": func() error {
			_, err := s.Patch(ctx, ws, m1, bad, wmk, api.PatchRequest{Patch: []api.PatchOp{{OldString: "first", NewString: "x"}}})
			return err
		},
		"append": func() error { _, err := s.Append(ctx, ws, m1, bad, wmk, api.AppendRequest{Content: "x"}); return err },
		"forget": func() error { _, err := s.Forget(ctx, ws, m1, bad); return err },
		"link": func() error {
			_, err := s.GraphLink(ctx, types.Edge{WorkspaceID: ws, SourceMemoryID: m2, TargetMemoryID: m1, EdgeType: types.EdgeTypeReferences, CreatedByAgentID: bad})
			return err
		},
		"link batch": func() error {
			_, err := s.GraphLinkBatch(ctx, []types.Edge{{WorkspaceID: ws, SourceMemoryID: m2, TargetMemoryID: m1, EdgeType: types.EdgeTypeMentions, CreatedByAgentID: bad}})
			return err
		},
		"unlink": func() error { return s.GraphUnlink(ctx, ws, edge.EdgeID, bad) },
	} {
		if err := write(); !errors.Is(err, types.ErrAgentDeactivated) {
			t.Errorf("%s by a deactivated agent: err = %v, want ErrAgentDeactivated", name, err)
		}
	}
	if got, _ := s.Metadata.GetMemory(ctx, m1); got.Content != "first" || got.HeadWatermark != wmk {
		t.Fatalf("a refused write changed the memory: %+v", got)
	}
	if err := s.TouchAgent(ctx, ws, bad, "opaque"); err != nil {
		t.Fatal(err)
	}
	if a, _ := s.Metadata.GetAgent(ctx, ws, bad); a.Active || a.DisplayName != "bad" {
		t.Fatalf("TouchAgent changed a deactivated agent: %+v", a)
	}
	if _, err := s.Imprint(ctx, ws, testAgent, api.ImprintRequest{Content: "an active agent still writes"}); err != nil {
		t.Fatal(err)
	}
}
