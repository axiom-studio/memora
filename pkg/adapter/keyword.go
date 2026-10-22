package adapter

import (
	"strings"
	"unicode"
)

// KeywordQuery is the input to MetadataStore.SearchKeyword. Its scope
// works like VectorQuery's: exactly one of WorkspaceIDs (those
// workspaces) or AllWorkspaces (the whole node).
type KeywordQuery struct {
	WorkspaceIDs  []string
	AllWorkspaces bool
	Query         string
	CollectionID  string
	// Filter's agent, time and tag fields are applied in the query, so
	// the K results are K matching memories (its CollectionID is unused;
	// set CollectionID above).
	Filter MemoryFilter
	K      int
}

// KeywordHit is one ranked live memory. Score is higher-is-better and
// only comparable within one result set.
type KeywordHit struct {
	MemoryID    string
	WorkspaceID string
	Score       float64
}

// Scope validates the query scope and returns the workspaces to search;
// nil means every workspace on the node.
func (q KeywordQuery) Scope() ([]string, error) {
	return searchScope("keyword", "", q.WorkspaceIDs, q.AllWorkspaces)
}

// maxKeywordTerms bounds how many terms one query contributes, so a long
// prompt cannot build an arbitrarily large full-text expression.
const maxKeywordTerms = 32

// KeywordTerms splits a free-text query into lowercase word terms (runs
// of letters and digits), de-duplicated, in order, at most 32. Stores
// build their full-text syntax from these terms, never from the raw
// query, so user input cannot inject full-text operators.
func KeywordTerms(query string) []string {
	fields := strings.FieldsFunc(strings.ToLower(query), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	seen := make(map[string]bool, len(fields))
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		if seen[f] {
			continue
		}
		seen[f] = true
		terms = append(terms, f)
		if len(terms) == maxKeywordTerms {
			break
		}
	}
	return terms
}
