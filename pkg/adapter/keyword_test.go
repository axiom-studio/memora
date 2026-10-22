package adapter

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestKeywordTerms(t *testing.T) {
	cases := map[string][]string{
		"Why did NVDA drop today?": {"why", "did", "nvda", "drop", "today"},
		`"quote" AND (x*) -y NEAR`: {"quote", "and", "x", "y", "near"},
		"dup Dup DUP":              {"dup"},
		"café 東京 v2":               {"café", "東京", "v2"},
		"   *** --- ":              {},
	}
	for in, want := range cases {
		if got := KeywordTerms(in); !reflect.DeepEqual(got, want) {
			t.Errorf("KeywordTerms(%q) = %q, want %q", in, got, want)
		}
	}
	var many []string
	for i := 0; i < 40; i++ {
		many = append(many, fmt.Sprintf("t%d", i))
	}
	if got := KeywordTerms(strings.Join(many, " ")); len(got) != maxKeywordTerms || got[0] != "t0" {
		t.Errorf("KeywordTerms(40 terms) = %d terms starting %q, want the first %d", len(got), got[0], maxKeywordTerms)
	}
}
