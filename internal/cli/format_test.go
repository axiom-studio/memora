package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestParseFormat(t *testing.T) {
	tests := []struct {
		in   string
		want Format
	}{
		{"json", FormatJSON},
		{"JSON", FormatJSON},
		{"jsonl", FormatJSONL},
		{"yaml", FormatYAML},
		{"text", FormatText},
		{"", FormatText},
		{"csv", FormatText},
	}
	for _, tt := range tests {
		if got := ParseFormat(tt.in); got != tt.want {
			t.Errorf("ParseFormat(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEmit_JSON(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatJSON, W: &buf}
	o.Emit(map[string]string{"hello": "world"})
	got := strings.TrimSpace(buf.String())
	if !strings.Contains(got, `"hello": "world"`) {
		t.Errorf("JSON output missing expected key: %s", got)
	}
}

func TestEmit_JSONL(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatJSONL, W: &buf}
	o.Emit(map[string]int{"a": 1})
	got := strings.TrimSpace(buf.String())
	if got != `{"a":1}` {
		t.Errorf("JSONL output = %q, want %q", got, `{"a":1}`)
	}
}

func TestEmit_YAML(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatYAML, W: &buf}
	o.Emit(map[string]any{"name": "test", "count": 42})
	got := buf.String()
	if !strings.Contains(got, "count: 42") {
		t.Errorf("YAML output missing 'count: 42': %s", got)
	}
	if !strings.Contains(got, "name: test") {
		t.Errorf("YAML output missing 'name: test': %s", got)
	}
}

func TestEmit_YAML_Nested(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatYAML, W: &buf}
	o.Emit(map[string]any{
		"outer": map[string]any{
			"inner": "value",
		},
	})
	got := buf.String()
	if !strings.Contains(got, "outer:") {
		t.Errorf("YAML missing 'outer:': %s", got)
	}
	if !strings.Contains(got, "  inner: value") {
		t.Errorf("YAML missing indented 'inner: value': %s", got)
	}
}

func TestEmit_YAML_List(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatYAML, W: &buf}
	o.Emit(map[string]any{
		"items": []string{"a", "b"},
	})
	got := buf.String()
	if !strings.Contains(got, "- a") {
		t.Errorf("YAML list missing '- a': %s", got)
	}
}

func TestEmit_Text(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatText, W: &buf}
	o.Emit("hello world")
	got := strings.TrimSpace(buf.String())
	if got != "hello world" {
		t.Errorf("text output = %q, want %q", got, "hello world")
	}
}

func TestEmit_Quiet(t *testing.T) {
	var buf bytes.Buffer
	o := &Output{Format: FormatJSON, Quiet: true, W: &buf}
	o.Emit(map[string]string{"hello": "world"})
	if buf.Len() != 0 {
		t.Errorf("quiet mode should produce no output, got %q", buf.String())
	}
}

func TestYAMLScalar_SpecialChars(t *testing.T) {
	got := yamlScalar("has: colon")
	if got != `"has: colon"` {
		t.Errorf("yamlScalar with colon = %q, want quoted", got)
	}
}

func TestYAMLScalar_Empty(t *testing.T) {
	got := yamlScalar("")
	if got != `""` {
		t.Errorf("yamlScalar empty = %q, want %q", got, `""`)
	}
}

func TestYAMLScalar_Nil(t *testing.T) {
	got := yamlScalar(nil)
	if got != "null" {
		t.Errorf("yamlScalar nil = %q, want %q", got, "null")
	}
}
