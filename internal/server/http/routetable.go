package http

import (
	"net/http"
	"strings"

	"github.com/axiom-studio/memora/pkg/types"
	"github.com/axiom-studio/memora/pkg/types/api"
)

// routeSpec declares one /v1 endpoint. routeTable is authoritative: the
// middleware answers 404 for a /v1 path no pattern matches and 405 for a
// method a matching pattern does not list, and it validates every ID
// path parameter before any handler runs. A new endpoint therefore has
// to be declared here to be reachable, and once declared the ID
// rejection test (routetable_test.go) covers it.
//
// Pattern segments are literals, "{name}" parameters, or a parameter
// with a literal suffix or second parameter: "{memory_id}:append",
// "{memory_id}@{watermark}". Parameters named *_id, and watermark, are
// UUIDv4s; others ({key}) are free text.
type routeSpec struct {
	Methods []string
	Pattern string
	// Bodies maps a method to its JSON request type; the type's *_id and
	// *_ids fields are IDs the handler must validate.
	Bodies map[string]any
	// QueryIDs are query parameters holding IDs.
	QueryIDs []string
}

var routeTable = []routeSpec{
	{Methods: []string{"POST"}, Pattern: "/v1/search", Bodies: map[string]any{"POST": api.SearchRequest{}}},
	{Methods: []string{"POST"}, Pattern: "/v1/federation/search", Bodies: map[string]any{"POST": api.FederationSearchRequest{}}},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces", Bodies: map[string]any{"POST": api.CreateWorkspaceRequest{}}},
	{Methods: []string{"GET", "PUT", "DELETE"}, Pattern: "/v1/workspaces/{workspace_id}", Bodies: map[string]any{"PUT": api.CreateWorkspaceRequest{}}},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces/{workspace_id}/collections", Bodies: map[string]any{"POST": api.CreateCollectionRequest{}}},
	{Methods: []string{"GET", "DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/collections/{collection_id}"},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces/{workspace_id}/memories", Bodies: map[string]any{"POST": api.ImprintRequest{}}, QueryIDs: []string{"collection_id"}},
	{Methods: []string{"GET", "PUT", "PATCH", "DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}", Bodies: map[string]any{"PUT": api.UpdateRequest{}, "PATCH": api.PatchRequest{}}},
	{Methods: []string{"POST"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}:append", Bodies: map[string]any{"POST": api.AppendRequest{}}},
	{Methods: []string{"GET"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}@{watermark}"},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}/edges", Bodies: map[string]any{"POST": api.LinkRequest{}}},
	{Methods: []string{"GET"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}/watermarks"},
	{Methods: []string{"PUT", "POST", "DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/memories/{memory_id}/tags/{key}"},
	{Methods: []string{"POST"}, Pattern: "/v1/workspaces/{workspace_id}/edges/:batch", Bodies: map[string]any{"POST": api.LinkBatchRequest{}}},
	{Methods: []string{"DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/edges/{edge_id}"},
	{Methods: []string{"POST"}, Pattern: "/v1/workspaces/{workspace_id}/graph/neighbors", Bodies: map[string]any{"POST": api.NeighborsRequest{}}},
	{Methods: []string{"POST"}, Pattern: "/v1/workspaces/{workspace_id}/graph/traverse", Bodies: map[string]any{"POST": api.TraverseRequest{}}},
	{Methods: []string{"GET"}, Pattern: "/v1/workspaces/{workspace_id}/graph/stats"},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces/{workspace_id}/agents", Bodies: map[string]any{"POST": api.RegisterAgentRequest{}}},
	{Methods: []string{"GET", "DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/agents/{agent_id}"},
	{Methods: []string{"POST"}, Pattern: "/v1/workspaces/{workspace_id}/recall", Bodies: map[string]any{"POST": api.RecallRequest{}}},
	{Methods: []string{"GET", "POST"}, Pattern: "/v1/workspaces/{workspace_id}/recall/pins", Bodies: map[string]any{"POST": api.PinRequest{}}},
	{Methods: []string{"DELETE"}, Pattern: "/v1/workspaces/{workspace_id}/recall/pins/{pin_id}"},
	{Methods: []string{"GET"}, Pattern: "/v1/workspaces/{workspace_id}/ledger", QueryIDs: []string{"memory_id", "since_ledger_id", "agent_id", "edge_id"}},
}

// isIDParam reports whether a path parameter holds a UUIDv4.
func isIDParam(name string) bool { return strings.HasSuffix(name, "_id") || name == "watermark" }

// matchRoute finds the routeTable entry for path. It returns the spec and
// its parameters, or nil when no pattern matches.
func matchRoute(path string) (*routeSpec, map[string]string) {
	segs := strings.Split(strings.Trim(path, "/"), "/")
	for i := range routeTable {
		if params, ok := matchPattern(routeTable[i].Pattern, segs); ok {
			return &routeTable[i], params
		}
	}
	return nil, nil
}

func matchPattern(pattern string, segs []string) (map[string]string, bool) {
	pat := strings.Split(strings.Trim(pattern, "/"), "/")
	if len(pat) != len(segs) {
		return nil, false
	}
	params := map[string]string{}
	for i, p := range pat {
		if !matchSegment(p, segs[i], params) {
			return nil, false
		}
	}
	return params, true
}

// matchSegment matches one segment: a literal, "{a}", "{a}<lit>" or
// "{a}<sep>{b}" where sep is ':' or '@'.
func matchSegment(p, seg string, params map[string]string) bool {
	if !strings.HasPrefix(p, "{") {
		return p == seg
	}
	end := strings.IndexByte(p, '}')
	name, rest := p[1:end], p[end+1:]
	switch {
	case rest == "":
		// An ID never holds ':' or '@', so "{memory_id}" leaves
		// "x:append" and "x@w" to their own patterns.
		if seg == "" || isIDParam(name) && strings.ContainsAny(seg, ":@") {
			return false
		}
		params[name] = seg
		return true
	case strings.HasPrefix(rest, "@{"):
		i := strings.IndexByte(seg, '@')
		if i <= 0 {
			return false
		}
		params[name], params[rest[2:len(rest)-1]] = seg[:i], seg[i+1:]
		return true
	default: // literal suffix such as ":append"
		if !strings.HasSuffix(seg, rest) || len(seg) == len(rest) {
			return false
		}
		params[name] = strings.TrimSuffix(seg, rest)
		return true
	}
}

// checkRoute enforces routeTable for a /v1 request: it writes the 404,
// 405 or 400 and returns false when the request must stop here.
func (s *Server) checkRoute(w http.ResponseWriter, r *http.Request) bool {
	spec, params := matchRoute(r.URL.Path)
	if spec == nil {
		s.writeError(w, 404, "not_found", "unknown route", map[string]any{"path": r.URL.Path})
		return false
	}
	allowed := false
	for _, m := range spec.Methods {
		allowed = allowed || m == r.Method
	}
	if !allowed {
		w.Header().Set("Allow", strings.Join(spec.Methods, ", "))
		s.writeError(w, 405, "method_not_allowed", "", nil)
		return false
	}
	// Path order, so the first bad segment is the one reported.
	for _, seg := range strings.Split(spec.Pattern, "/") {
		for _, part := range strings.FieldsFunc(seg, func(c rune) bool { return c == '{' || c == '}' || c == '@' || c == ':' }) {
			if v, ok := params[part]; ok && isIDParam(part) {
				if _, err := types.ParseID(part, v); err != nil {
					s.writeErrorFromService(w, err)
					return false
				}
			}
		}
	}
	return true
}
