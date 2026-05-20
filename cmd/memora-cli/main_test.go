package main

import (
	"testing"
)

func TestParseGlobal_Defaults(t *testing.T) {
	g := parseGlobal(nil)
	if g.Endpoint != "http://localhost:7777" && g.Endpoint == "" {
		t.Errorf("expected non-empty default endpoint, got %q", g.Endpoint)
	}
	if g.Output != "text" && g.Output == "" {
		t.Errorf("expected non-empty default output, got %q", g.Output)
	}
	if g.Quiet || g.Verbose || g.NoColor {
		t.Error("boolean flags should default to false")
	}
}

func TestParseGlobal_AllFlags(t *testing.T) {
	args := []string{
		"--endpoint", "http://example.com",
		"--api-key", "secret",
		"--agent-id", "test-agent",
		"--workspace", "ws-1",
		"--output", "json",
		"--timeout", "10s",
		"--no-color",
		"--quiet",
		"--verbose",
		"positional",
	}
	g := parseGlobal(args)
	if g.Endpoint != "http://example.com" {
		t.Errorf("endpoint = %q, want %q", g.Endpoint, "http://example.com")
	}
	if g.APIKey != "secret" {
		t.Errorf("api-key = %q, want %q", g.APIKey, "secret")
	}
	if g.AgentID != "test-agent" {
		t.Errorf("agent-id = %q, want %q", g.AgentID, "test-agent")
	}
	if g.Workspace != "ws-1" {
		t.Errorf("workspace = %q, want %q", g.Workspace, "ws-1")
	}
	if g.Output != "json" {
		t.Errorf("output = %q, want %q", g.Output, "json")
	}
	if g.Timeout.Seconds() != 10 {
		t.Errorf("timeout = %v, want 10s", g.Timeout)
	}
	if !g.NoColor {
		t.Error("expected --no-color to be true")
	}
	if !g.Quiet {
		t.Error("expected --quiet to be true")
	}
	if !g.Verbose {
		t.Error("expected --verbose to be true")
	}
	if len(g.Args) != 1 || g.Args[0] != "positional" {
		t.Errorf("positional args = %v, want [positional]", g.Args)
	}
}

func TestParseGlobal_ShortFlags(t *testing.T) {
	g := parseGlobal([]string{"-w", "ws-2", "-o", "yaml", "-q", "-v"})
	if g.Workspace != "ws-2" {
		t.Errorf("workspace = %q, want %q", g.Workspace, "ws-2")
	}
	if g.Output != "yaml" {
		t.Errorf("output = %q, want %q", g.Output, "yaml")
	}
	if !g.Quiet {
		t.Error("expected -q to set Quiet")
	}
	if !g.Verbose {
		t.Error("expected -v to set Verbose")
	}
}

func TestExtractCommand_FlagsBeforeCommand(t *testing.T) {
	cmd, rest := extractCommand([]string{"--api-key", "secret", "--verbose", "workspaces", "list"})
	if cmd != "workspaces" {
		t.Errorf("cmd = %q, want %q", cmd, "workspaces")
	}
	g := parseGlobal(rest)
	if g.APIKey != "secret" {
		t.Errorf("api-key = %q, want %q", g.APIKey, "secret")
	}
	if !g.Verbose {
		t.Error("expected --verbose to be true")
	}
	if len(g.Args) != 1 || g.Args[0] != "list" {
		t.Errorf("positional args = %v, want [list]", g.Args)
	}
}

func TestExtractCommand_FlagsAfterCommand(t *testing.T) {
	cmd, rest := extractCommand([]string{"imprint", "--api-key", "secret", "--workspace", "ws1"})
	if cmd != "imprint" {
		t.Errorf("cmd = %q, want %q", cmd, "imprint")
	}
	g := parseGlobal(rest)
	if g.APIKey != "secret" {
		t.Errorf("api-key = %q, want %q", g.APIKey, "secret")
	}
	if g.Workspace != "ws1" {
		t.Errorf("workspace = %q, want %q", g.Workspace, "ws1")
	}
}

func TestExtractCommand_MixedFlags(t *testing.T) {
	cmd, rest := extractCommand([]string{"--endpoint", "http://host:8080", "-q", "recall", "query text"})
	if cmd != "recall" {
		t.Errorf("cmd = %q, want %q", cmd, "recall")
	}
	g := parseGlobal(rest)
	if g.Endpoint != "http://host:8080" {
		t.Errorf("endpoint = %q, want %q", g.Endpoint, "http://host:8080")
	}
	if !g.Quiet {
		t.Error("expected -q to set Quiet")
	}
	if len(g.Args) != 1 || g.Args[0] != "query text" {
		t.Errorf("positional args = %v, want [query text]", g.Args)
	}
}

func TestExtractCommand_NoCommand(t *testing.T) {
	cmd, _ := extractCommand([]string{"--api-key", "secret"})
	if cmd != "" {
		t.Errorf("cmd = %q, want empty", cmd)
	}
}

func TestExtractCommand_Empty(t *testing.T) {
	cmd, _ := extractCommand(nil)
	if cmd != "" {
		t.Errorf("cmd = %q, want empty", cmd)
	}
}

func TestExtractCommand_CommandOnly(t *testing.T) {
	cmd, rest := extractCommand([]string{"health"})
	if cmd != "health" {
		t.Errorf("cmd = %q, want %q", cmd, "health")
	}
	if len(rest) != 0 {
		t.Errorf("rest = %v, want empty", rest)
	}
}

func TestPopPositional(t *testing.T) {
	pos, rest := popPositional([]string{"abc", "--flag", "val"})
	if pos != "abc" {
		t.Errorf("popPositional pos = %q, want %q", pos, "abc")
	}
	if len(rest) != 2 || rest[0] != "--flag" {
		t.Errorf("popPositional rest = %v, want [--flag val]", rest)
	}
}

func TestPopPositional_Empty(t *testing.T) {
	pos, rest := popPositional(nil)
	if pos != "" {
		t.Errorf("popPositional empty = %q, want empty", pos)
	}
	if len(rest) != 0 {
		t.Errorf("popPositional empty rest = %v, want nil", rest)
	}
}

func TestPopPositional_FlagsOnly(t *testing.T) {
	// popPositional skips dash-prefixed args; "val" is not dash-prefixed
	// so it's treated as a positional even after --flag.
	pos, rest := popPositional([]string{"--flag", "val"})
	if pos != "val" {
		t.Errorf("popPositional flags-only = %q, want %q", pos, "val")
	}
	if len(rest) != 1 || rest[0] != "--flag" {
		t.Errorf("rest = %v, want [--flag]", rest)
	}
}

func TestPopTwoPositionals(t *testing.T) {
	pos, rest := popTwoPositionals([]string{"a", "b", "--flag"})
	if len(pos) != 2 || pos[0] != "a" || pos[1] != "b" {
		t.Errorf("popTwoPositionals = %v, want [a b]", pos)
	}
	if len(rest) != 1 || rest[0] != "--flag" {
		t.Errorf("popTwoPositionals rest = %v, want [--flag]", rest)
	}
}

func TestTruncate(t *testing.T) {
	if got := truncate("hello", 10); got != "hello" {
		t.Errorf("truncate short = %q", got)
	}
	if got := truncate("hello world", 5); got != "hello…" {
		t.Errorf("truncate long = %q", got)
	}
}

func TestLoadContent_Inline(t *testing.T) {
	got, err := loadContent("inline text", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "inline text" {
		t.Errorf("loadContent = %q, want %q", got, "inline text")
	}
}

func TestLoadContent_Empty(t *testing.T) {
	got, err := loadContent("", "")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("loadContent empty = %q, want empty", got)
	}
}

func TestLoadContent_File(t *testing.T) {
	got, err := loadContent("", "/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("loadContent /dev/null = %q, want empty", got)
	}
}

func TestLoadContent_BadFile(t *testing.T) {
	_, err := loadContent("", "/nonexistent/path/file.txt")
	if err == nil {
		t.Error("expected error for nonexistent file")
	}
}

func TestRepeatable(t *testing.T) {
	r := newRepeatable()
	_ = r.Set("a=1")
	_ = r.Set("b=2")
	if len(r.values) != 2 {
		t.Errorf("repeatable len = %d, want 2", len(r.values))
	}
	if r.String() != "a=1,b=2" {
		t.Errorf("repeatable String = %q", r.String())
	}
}
