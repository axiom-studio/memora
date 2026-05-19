package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
)

type Format string

const (
	FormatText Format = "text"
	FormatJSON Format = "json"
	FormatJSONL Format = "jsonl"
	FormatYAML Format = "yaml"
)

func ParseFormat(s string) Format {
	switch strings.ToLower(s) {
	case "json":
		return FormatJSON
	case "jsonl":
		return FormatJSONL
	case "yaml":
		return FormatYAML
	default:
		return FormatText
	}
}

type Output struct {
	Format  Format
	NoColor bool
	Quiet   bool
	Verbose bool
	W       io.Writer
}

func NewOutput(format Format, noColor, quiet, verbose bool) *Output {
	return &Output{
		Format:  format,
		NoColor: noColor,
		Quiet:   quiet,
		Verbose: verbose,
		W:       os.Stdout,
	}
}

func (o *Output) Emit(v any) {
	if o.Quiet {
		return
	}
	switch o.Format {
	case FormatJSON:
		enc := json.NewEncoder(o.W)
		enc.SetIndent("", "  ")
		_ = enc.Encode(v)
	case FormatJSONL:
		b, _ := json.Marshal(v)
		fmt.Fprintln(o.W, string(b))
	case FormatYAML:
		emitYAML(o.W, v, 0)
	default:
		emitText(o.W, v)
	}
}

func emitText(w io.Writer, v any) {
	switch t := v.(type) {
	case string:
		fmt.Fprintln(w, t)
	default:
		b, _ := json.MarshalIndent(t, "", "  ")
		fmt.Fprintln(w, string(b))
	}
}

// emitYAML produces a simple YAML-like output without external deps.
// Handles maps, slices, and scalars via a JSON round-trip to
// map[string]any.
func emitYAML(w io.Writer, v any, indent int) {
	m := toGeneric(v)
	writeYAML(w, m, indent)
}

func writeYAML(w io.Writer, v any, indent int) {
	prefix := strings.Repeat("  ", indent)
	switch t := v.(type) {
	case map[string]any:
		keys := sortedKeys(t)
		for _, k := range keys {
			child := t[k]
			if isScalar(child) {
				fmt.Fprintf(w, "%s%s: %s\n", prefix, k, yamlScalar(child))
			} else {
				fmt.Fprintf(w, "%s%s:\n", prefix, k)
				writeYAML(w, child, indent+1)
			}
		}
	case []any:
		for _, item := range t {
			if isScalar(item) {
				fmt.Fprintf(w, "%s- %s\n", prefix, yamlScalar(item))
			} else {
				fmt.Fprintf(w, "%s-\n", prefix)
				writeYAML(w, item, indent+1)
			}
		}
	default:
		fmt.Fprintf(w, "%s%s\n", prefix, yamlScalar(t))
	}
}

func toGeneric(v any) any {
	b, err := json.Marshal(v)
	if err != nil {
		return v
	}
	var out any
	if err := json.Unmarshal(b, &out); err != nil {
		return v
	}
	return out
}

func isScalar(v any) bool {
	if v == nil {
		return true
	}
	rt := reflect.TypeOf(v)
	return rt.Kind() != reflect.Map && rt.Kind() != reflect.Slice
}

func yamlScalar(v any) string {
	if v == nil {
		return "null"
	}
	switch t := v.(type) {
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		if t == float64(int64(t)) {
			return fmt.Sprintf("%d", int64(t))
		}
		return fmt.Sprintf("%g", t)
	case string:
		if t == "" {
			return `""`
		}
		if strings.ContainsAny(t, ":\n#{}[]|>&*!%@`") {
			b, _ := json.Marshal(t)
			return string(b)
		}
		return t
	default:
		return fmt.Sprint(v)
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
