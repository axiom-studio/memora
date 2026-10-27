package postgres

import (
	"context"
	"fmt"
	"testing"

	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/types"
)

// TestMigration_TagsJSONRebuilt_Postgres (#6182): diverged tags_json is
// rebuilt from memora_tags when 0010 runs.
func TestMigration_TagsJSONRebuilt_Postgres(t *testing.T) {
	dsn := skipWithoutPostgres(t)
	ctx := context.Background()
	open := func() *MetadataStore {
		t.Helper()
		s := &MetadataStore{}
		if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
			t.Fatal(err)
		}
		return s
	}
	s := open()
	ws := &types.Workspace{Name: "tags-mig-" + types.NewID()}
	if err := s.CreateWorkspace(ctx, ws); err != nil {
		t.Fatal(err)
	}
	edited := &types.Memory{WorkspaceID: ws.ID, Content: "edited", WrittenByAgentID: types.NewID(), Tags: map[string]string{"k": "v"}}
	cleared := &types.Memory{WorkspaceID: ws.ID, Content: "cleared", WrittenByAgentID: types.NewID(), Tags: map[string]string{"gone": "x"}}
	for _, m := range []*types.Memory{edited, cleared} {
		if _, err := s.ImprintMemory(ctx, m); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		fmt.Sprintf(`INSERT INTO memora_tags(memory_id, key, value) VALUES ('%s', 'team', 'red')`, edited.ID),
		fmt.Sprintf(`DELETE FROM memora_tags WHERE memory_id = '%s' AND key = 'k'`, edited.ID),
		fmt.Sprintf(`DELETE FROM memora_tags WHERE memory_id = '%s'`, cleared.ID),
		`DELETE FROM memora_schema_migrations WHERE version = '0010_tags_json_sync.sql'`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	s.Close()

	s = open()
	defer s.Close()
	for m, want := range map[*types.Memory]string{edited: "map[team:red]", cleared: "map[]"} {
		got, err := s.GetMemory(ctx, m.ID)
		if err != nil {
			t.Fatal(err)
		}
		if fmt.Sprint(got.Tags) != want {
			t.Errorf("%s: tags after migration = %v, want %s", m.Content, got.Tags, want)
		}
	}
}
