package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestMigration_TagsJSONRebuilt (#6182): a database whose tags_json
// diverged from memora_tags (tag edits by an older build) is reconciled
// from memora_tags when 0013 runs.
func TestMigration_TagsJSONRebuilt(t *testing.T) {
	ctx := context.Background()
	dsn := filepath.Join(t.TempDir(), "m.db")
	open := func() *Store {
		t.Helper()
		s := &Store{}
		if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	ws := &types.Workspace{Name: "w"}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	edited := &types.Memory{WorkspaceID: ws.ID, Content: "edited", WrittenByAgentID: types.NewID(), Tags: map[string]string{"k": "v"}}
	cleared := &types.Memory{WorkspaceID: ws.ID, Content: "cleared", WrittenByAgentID: types.NewID(), Tags: map[string]string{"gone": "x"}}
	untouched := &types.Memory{WorkspaceID: ws.ID, Content: "untouched", WrittenByAgentID: types.NewID(), Tags: map[string]string{"a": "b"}}
	for _, m := range []*types.Memory{edited, cleared, untouched} {
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	// What the old UpsertTag/DeleteTag left behind: memora_tags edited,
	// tags_json not; then roll the migration back so it runs again.
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO memora_tags(memory_id, key, value) VALUES ('%s', 'team', 'red')`, edited.ID),
		fmt.Sprintf(`DELETE FROM memora_tags WHERE memory_id = '%s' AND key = 'k'`, edited.ID),
		fmt.Sprintf(`DELETE FROM memora_tags WHERE memory_id = '%s'`, cleared.ID),
		`DELETE FROM memora_schema_migrations WHERE version = '0013_tags_json_sync.sql'`,
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if m, _ := s.GetMemory(ctx, edited.ID); m.Tags["k"] != "v" {
		t.Fatalf("setup: tags_json = %v, want the stale k=v", m.Tags)
	}
	_ = s.Close()

	s = open()
	defer s.Close()
	for _, c := range []struct {
		m    *types.Memory
		want string
	}{{edited, "map[team:red]"}, {cleared, "map[]"}, {untouched, "map[a:b]"}} {
		got, err := s.GetMemory(ctx, c.m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got.Tags) != c.want {
			t.Errorf("%s: tags after migration = %v, want %s", c.m.Content, got.Tags, c.want)
		}
	}
}
