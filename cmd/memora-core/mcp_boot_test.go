package main

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/axiom-studio/memora/internal/config"
)

// TestRunMCP_RejectsSqliteGraphWithoutSqliteMetadata runs the real mcp boot
// path in a subprocess: it must refuse the config with EX_CONFIG instead of
// booting and failing on the first graph call (Issue #4378, QA rejection).
func TestRunMCP_RejectsSqliteGraphWithoutSqliteMetadata(t *testing.T) {
	if os.Getenv("MEMORA_TEST_RUN_MCP") == "1" {
		os.Args = []string{"memora-core", "--data-dir", os.Getenv("MEMORA_TEST_DATA_DIR")}
		runMCP()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestRunMCP_RejectsSqliteGraphWithoutSqliteMetadata$")
	cmd.Env = append(os.Environ(),
		"MEMORA_TEST_RUN_MCP=1",
		"MEMORA_TEST_DATA_DIR="+t.TempDir(),
		"MEMORA_CONFIG=",
		"MEMORA_METADATA_DRIVER=postgres",
		"MEMORA_METADATA_DSN=postgres://unused@127.0.0.1:1/none?sslmode=disable",
		"MEMORA_GRAPH_DRIVER=sqlite_graph",
	)
	cmd.Stdin = strings.NewReader("")
	out, err := cmd.CombinedOutput()

	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != config.EX_CONFIG {
		t.Fatalf("mcp exit = %v, want exit %d; output:\n%s", err, config.EX_CONFIG, out)
	}
	if !strings.Contains(string(out), `graph_driver = "none"`) {
		t.Fatalf("mcp output should name the remedy; got:\n%s", out)
	}
}

// serve must exit with EX_CONFIG too; it used Fatalf, which exits 1.
func TestServe_InvalidConfigExitsEXConfig(t *testing.T) {
	if os.Getenv("MEMORA_TEST_RUN_SERVE") == "1" {
		os.Args = []string{"memora-core", "--data-dir", os.Getenv("MEMORA_TEST_DATA_DIR"), "--addr", "127.0.0.1:0"}
		serve()
		return
	}
	cmd := exec.Command(os.Args[0], "-test.run=^TestServe_InvalidConfigExitsEXConfig$")
	cmd.Env = append(os.Environ(),
		"MEMORA_TEST_RUN_SERVE=1",
		"MEMORA_TEST_DATA_DIR="+t.TempDir(),
		"MEMORA_CONFIG=",
		"MEMORA_METADATA_DRIVER=postgres",
		"MEMORA_GRAPH_DRIVER=sqlite_graph",
	)
	out, err := cmd.CombinedOutput()
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) || exitErr.ExitCode() != config.EX_CONFIG {
		t.Fatalf("serve exit = %v, want exit %d; output:\n%s", err, config.EX_CONFIG, out)
	}
}

// MEMORA_GRAPH_DRIVER=none is the env-expressible way to disable the graph.
func TestRunMCP_GraphDriverNoneDisablesGraph(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.MetadataDriver = "sqlite"
	cfg.Storage.GraphDriver = "none"
	svc, closeAll, err := openCoreAdapters(t.Context(), cfg, discardLogger())
	if err != nil {
		t.Fatalf("openCoreAdapters with graph_driver=none: %v", err)
	}
	defer closeAll()
	if svc.Graph != nil {
		t.Fatal("graph_driver=none opened a graph store")
	}
}

func TestOpenCoreAdapters_RejectsInvalidConfigBeforeOpening(t *testing.T) {
	cfg := testAdapterConfig(t)
	cfg.Storage.MetadataDriver = "postgres"
	cfg.Storage.MetadataDSN = "postgres://unused@127.0.0.1:1/none?sslmode=disable"
	_, _, err := openCoreAdapters(t.Context(), cfg, discardLogger())
	if err == nil || !strings.Contains(err.Error(), "config validation failed") {
		t.Fatalf("err = %v, want config validation failure before any adapter is opened", err)
	}
}
