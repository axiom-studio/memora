package postgres

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/axiom-studio/memora/pkg/adapter"
)

// freshDatabase creates a throwaway database next to MEMORA_POSTGRES_DSN
// and returns its DSN.
func freshDatabase(t *testing.T) string {
	t.Helper()
	base := skipWithoutPostgres(t)
	ctx := context.Background()
	admin, err := pgx.Connect(ctx, base)
	if err != nil {
		t.Fatal(err)
	}
	name := fmt.Sprintf("memora_legacy_%d", time.Now().UnixNano())
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = admin.Exec(context.Background(), "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
		_ = admin.Close(context.Background())
	})
	u, err := url.Parse(base)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	return u.String()
}

// TestOpen_LegacyIDsRefusedReadOnly_Postgres (#4377): a database with a
// pre-UUID workspace is refused before any migration or seeding.
func TestOpen_LegacyIDsRefusedReadOnly_Postgres(t *testing.T) {
	dsn := freshDatabase(t)
	ctx := context.Background()
	s := &MetadataStore{}
	if err := s.Open(ctx, adapter.MetadataConfig{DSN: dsn}); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{
		`DELETE FROM memora_schema_migrations WHERE version IN ('0008_federation_peers.sql', '0009_peer_inbound_qps.sql')`,
		`DROP TABLE memora_federation_peers`,
		`INSERT INTO memora_workspaces (id, name, created_at, updated_at) VALUES ('ws_01HXYZABCDEFGHJKMNPQRSTVWX', 'old', now(), now())`,
		`DELETE FROM memora_agents`,
	} {
		if _, err := s.pool.Exec(ctx, q); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	_ = s.Close()

	snapshot := func() string {
		t.Helper()
		conn, err := pgx.Connect(ctx, dsn)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close(ctx)
		var out strings.Builder
		for _, q := range []string{
			`SELECT string_agg(table_name, ',' ORDER BY table_name) FROM information_schema.tables WHERE table_schema = 'public'`,
			`SELECT string_agg(version, ',' ORDER BY version) FROM memora_schema_migrations`,
			`SELECT count(*)::text FROM memora_agents`,
		} {
			var v *string
			if err := conn.QueryRow(ctx, q).Scan(&v); err != nil {
				t.Fatal(err)
			}
			if v != nil {
				out.WriteString(*v)
			}
			out.WriteString("\n")
		}
		return out.String()
	}
	before := snapshot()
	err := (&MetadataStore{}).Open(ctx, adapter.MetadataConfig{DSN: dsn})
	if !errors.Is(err, adapter.ErrLegacyIDs) || !strings.Contains(err.Error(), "ws_01HXYZABCDEFGHJKMNPQRSTVWX") {
		t.Fatalf("Open on a legacy database = %v, want ErrLegacyIDs naming the workspace", err)
	}
	if after := snapshot(); after != before {
		t.Fatalf("the refused Open changed the database:\nbefore %s\nafter  %s", before, after)
	}
}
