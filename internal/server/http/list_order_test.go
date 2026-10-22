package http_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/axiom-studio/memora/pkg/client"
)

// TestList_E2E_ChronologicalPaging (ID-6): over HTTP, memories and
// workspaces list newest first and page with next_cursor; the SDK page
// methods walk the same pages; a bad cursor is 400. Before, the
// workspace route returned a next_cursor it then ignored.
func TestList_E2E_ChronologicalPaging(t *testing.T) {
	ts := newTestServer(t)
	c := ts.Client()
	base := ts.URL + "/v1/workspaces"
	agentH := map[string]string{"Memora-Agent-Id": "2b4224a8-95b4-4171-9e6b-689847d53ac7"}

	var wsIDs []string
	for i := 0; i < 5; i++ {
		_, ws := doJSON(t, c, jsonReq(t, "POST", base, map[string]any{"name": fmt.Sprintf("ws-%d", i)}, nil))
		wsIDs = append(wsIDs, ws["id"].(string))
	}
	var memIDs []string
	for i := 0; i < 5; i++ {
		status, m := doJSON(t, c, jsonReq(t, "POST", base+"/"+wsIDs[0]+"/memories", map[string]any{"content": fmt.Sprintf("memory %d", i)}, agentH))
		if status != 201 {
			t.Fatalf("imprint: %d %v", status, m)
		}
		memIDs = append(memIDs, m["id"].(string))
	}
	reverse := func(in []string) []string {
		out := make([]string, len(in))
		for i, v := range in {
			out[len(in)-1-i] = v
		}
		return out
	}

	walk := func(path, key string) []string {
		var got []string
		cursor := ""
		for page := 0; page < 10; page++ {
			u := path + "?limit=2"
			if cursor != "" {
				u += "&cursor=" + cursor
			}
			status, body := doJSON(t, c, jsonReq(t, "GET", u, nil, nil))
			if status != 200 {
				t.Fatalf("GET %s: %d %v", u, status, body)
			}
			for _, item := range body[key].([]any) {
				got = append(got, item.(map[string]any)["id"].(string))
			}
			next, _ := body["next_cursor"].(string)
			if next == "" {
				return got
			}
			cursor = next
		}
		t.Fatalf("GET %s: cursor never ended", path)
		return nil
	}
	if got, want := walk(base+"/"+wsIDs[0]+"/memories", "memories"), reverse(memIDs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("memories = %v, want newest first %v", got, want)
	}
	if got, want := walk(base, "workspaces"), reverse(wsIDs); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("workspaces = %v, want newest first %v", got, want)
	}

	sdk := client.New(ts.URL, "")
	var got []string
	cursor := ""
	for {
		mems, next, err := sdk.ListMemoriesPage(context.Background(), wsIDs[0], "", cursor, 2)
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range mems {
			got = append(got, m.ID)
		}
		if next == "" {
			break
		}
		cursor = next
	}
	if fmt.Sprint(got) != fmt.Sprint(reverse(memIDs)) {
		t.Fatalf("SDK pages = %v, want %v", got, reverse(memIDs))
	}

	for _, u := range []string{base + "?cursor=garbage", base + "/" + wsIDs[0] + "/memories?cursor=garbage"} {
		if status, body := doJSON(t, c, jsonReq(t, "GET", u, nil, nil)); status != 400 || body["error_code"] != "invalid_input" {
			t.Fatalf("GET %s: %d %v, want 400 invalid_input", u, status, body)
		}
	}
}
