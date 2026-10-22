// memora-cli is the user-facing command-line tool for Memora. It
// targets both local memora-core servers (default
// http://localhost:7777) and remote Memora Cloud endpoints (via
// --endpoint or MEMORA_ENDPOINT).
//
// Design note: CLI dispatch uses stdlib flag (not Cobra). This is a
// deliberate zero-dependency choice for OSS v0.1. Trade-offs accepted:
// no auto-generated completion, no subcommand grouping. If these become
// blockers, Cobra migration is tracked in PRD §9.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	mcli "github.com/axiom-studio/memora/internal/cli"
	"github.com/axiom-studio/memora/pkg/adapter"
	"github.com/axiom-studio/memora/pkg/client"
	"github.com/axiom-studio/memora/pkg/types/api"

	// Register adapters for the migrate subcommand.
	_ "github.com/axiom-studio/memora/internal/store/file"
	_ "github.com/axiom-studio/memora/internal/store/sqlite"
)

var (
	version   = "dev"
	commit    = ""
	buildDate = ""
)

const exitUsage = 1
const exitAuth = 2
const exitServer = 3
const exitClient = 4
const exitCAS = 5
const exitNotFound = 8

// defaultAgentID is the agent the CLI writes as when neither
// --agent-id nor $MEMORA_AGENT_ID is set: one fixed UUIDv4 shared by
// every local CLI user, as "agent_opaque_local" was before IDs were UUIDs.
const defaultAgentID = "c1a0e5d2-7b3f-4a6e-9d8c-1f2e3a4b5c6d"

func main() {
	if len(os.Args) < 2 || os.Args[1] == "--help" || os.Args[1] == "-h" {
		printRootHelp()
		return
	}

	cmd, rest := extractCommand(os.Args[1:])
	if cmd == "" || cmd == "--help" || cmd == "-h" {
		printRootHelp()
		return
	}
	if cmd == "version" {
		fmt.Printf("memora-cli %s (commit=%s built=%s)\n", version, commit, buildDate)
		return
	}

	g := parseGlobal(rest)
	c := client.New(g.Endpoint, g.APIKey)
	c.AgentID = g.AgentID
	c.Workspace = g.Workspace
	g.out = mcli.NewOutput(mcli.ParseFormat(g.Output), g.NoColor, g.Quiet, g.Verbose)

	ctx, cancel := context.WithTimeout(context.Background(), g.Timeout)
	defer cancel()

	switch cmd {
	case "migrate":
		cmdMigrate(ctx, g)
		return
	case "workspaces":
		cmdWorkspaces(ctx, c, g)
	case "collections":
		cmdCollections(ctx, c, g)
	case "imprint":
		cmdImprint(ctx, c, g)
	case "lookup":
		cmdLookup(ctx, c, g)
	case "update":
		cmdUpdate(ctx, c, g)
	case "patch":
		cmdPatch(ctx, c, g)
	case "append":
		cmdAppend(ctx, c, g)
	case "forget":
		cmdForget(ctx, c, g)
	case "recall":
		cmdRecall(ctx, c, g)
	case "search":
		cmdSearch(ctx, c, g)
	case "fetch":
		cmdFetch(ctx, c, g)
	case "list":
		cmdList(ctx, c, g)
	case "watermarks":
		cmdWatermarks(ctx, c, g)
	case "link":
		cmdLink(ctx, c, g)
	case "unlink":
		cmdUnlink(ctx, c, g)
	case "neighbors":
		cmdNeighbors(ctx, c, g)
	case "traverse":
		cmdTraverse(ctx, c, g)
	case "graph":
		cmdGraph(ctx, c, g)
	case "agents":
		cmdAgents(ctx, c, g)
	case "pin":
		cmdPin(ctx, c, g)
	case "health":
		cmdHealth(ctx, c, g)
	case "ready":
		cmdReady(ctx, c, g)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q (try memora-cli --help)\n", cmd)
		os.Exit(exitUsage)
	}
}

type globalFlags struct {
	Endpoint  string
	APIKey    string
	AgentID   string
	Workspace string
	Output    string
	Timeout   time.Duration
	NoColor   bool
	Quiet     bool
	Verbose   bool
	Args      []string
	out       *mcli.Output
}

func parseGlobal(args []string) globalFlags {
	g := globalFlags{
		Endpoint:  getenv("MEMORA_ENDPOINT", "http://localhost:7777"),
		APIKey:    os.Getenv("MEMORA_API_KEY"),
		AgentID:   getenv("MEMORA_AGENT_ID", defaultAgentID),
		Workspace: os.Getenv("MEMORA_WORKSPACE"),
		Output:    getenv("MEMORA_OUTPUT", "text"),
		Timeout:   30 * time.Second,
	}
	out := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--endpoint":
			if i+1 < len(args) {
				g.Endpoint = args[i+1]
				i++
			}
		case "--api-key":
			if i+1 < len(args) {
				g.APIKey = args[i+1]
				i++
			}
		case "--agent-id":
			if i+1 < len(args) {
				g.AgentID = args[i+1]
				i++
			}
		case "--workspace", "-w":
			if i+1 < len(args) {
				g.Workspace = args[i+1]
				i++
			}
		case "--output", "-o":
			if i+1 < len(args) {
				g.Output = args[i+1]
				i++
			}
		case "--timeout":
			if i+1 < len(args) {
				if d, err := time.ParseDuration(args[i+1]); err == nil {
					g.Timeout = d
				}
				i++
			}
		case "--no-color":
			g.NoColor = true
		case "--quiet", "-q":
			g.Quiet = true
		case "--verbose", "-v":
			g.Verbose = true
		default:
			out = append(out, args[i])
		}
	}
	g.Args = out
	return g
}

var globalFlagsWithValue = map[string]bool{
	"--endpoint": true, "--api-key": true, "--agent-id": true,
	"--workspace": true, "-w": true, "--output": true, "-o": true,
	"--timeout": true,
}

var globalFlagsBool = map[string]bool{
	"--no-color": true, "--quiet": true, "-q": true,
	"--verbose": true, "-v": true,
}

func extractCommand(args []string) (cmd string, rest []string) {
	var before []string
	for i := 0; i < len(args); i++ {
		if globalFlagsWithValue[args[i]] {
			before = append(before, args[i])
			if i+1 < len(args) {
				i++
				before = append(before, args[i])
			}
			continue
		}
		if globalFlagsBool[args[i]] {
			before = append(before, args[i])
			continue
		}
		rest = append(before, args[i+1:]...)
		return args[i], rest
	}
	return "", before
}

func getenv(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func emit(g globalFlags, v any) {
	g.out.Emit(v)
}

func die(err error) {
	if err == nil {
		return
	}
	var apiErr *client.APIError
	if errors.As(err, &apiErr) {
		fmt.Fprintln(os.Stderr, "error:", apiErr.Error())
		switch {
		case apiErr.IsCAS():
			os.Exit(exitCAS)
		case apiErr.IsNotFound():
			os.Exit(exitNotFound)
		case apiErr.Status == 401:
			os.Exit(exitAuth)
		case apiErr.Status >= 500:
			os.Exit(exitServer)
		default:
			os.Exit(exitClient)
		}
	}
	fmt.Fprintln(os.Stderr, "error:", err.Error())
	os.Exit(exitClient)
}

// ----- subcommand: workspaces -----

func cmdWorkspaces(ctx context.Context, c *client.Client, g globalFlags) {
	if len(g.Args) == 0 {
		fmt.Println("usage: memora-cli workspaces <list|create|show|delete>")
		os.Exit(exitUsage)
	}
	switch g.Args[0] {
	case "list":
		ws, err := c.ListWorkspaces(ctx)
		die(err)
		emit(g, map[string]any{"workspaces": ws})
	case "create":
		fs := flag.NewFlagSet("workspaces create", flag.ExitOnError)
		name := fs.String("name", "", "workspace name (required)")
		region := fs.String("region", "", "region")
		_ = fs.Parse(g.Args[1:])
		if *name == "" {
			fmt.Fprintln(os.Stderr, "workspaces create: --name required")
			os.Exit(exitUsage)
		}
		ws, err := c.CreateWorkspace(ctx, api.CreateWorkspaceRequest{Name: *name, Region: *region})
		die(err)
		emit(g, ws)
	case "show":
		if len(g.Args) < 2 {
			fmt.Fprintln(os.Stderr, "workspaces show <ws_id>")
			os.Exit(exitUsage)
		}
		ws, err := c.GetWorkspace(ctx, g.Args[1])
		die(err)
		emit(g, ws)
	case "delete":
		if len(g.Args) < 2 {
			fmt.Fprintln(os.Stderr, "workspaces delete <ws_id>")
			os.Exit(exitUsage)
		}
		die(c.DeleteWorkspace(ctx, g.Args[1]))
		fmt.Println("✓ deleted", g.Args[1])
	default:
		fmt.Fprintf(os.Stderr, "unknown workspaces subcommand %q\n", g.Args[0])
		os.Exit(exitUsage)
	}
}

func cmdCollections(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Println("usage: memora-cli collections <list|create>")
		os.Exit(exitUsage)
	}
	switch g.Args[0] {
	case "list":
		cs, err := c.ListCollections(ctx, g.Workspace)
		die(err)
		emit(g, map[string]any{"collections": cs})
	case "create":
		if len(g.Args) < 2 {
			fmt.Fprintln(os.Stderr, "collections create <name>")
			os.Exit(exitUsage)
		}
		out, err := c.CreateCollection(ctx, g.Workspace, g.Args[1])
		die(err)
		emit(g, out)
	default:
		fmt.Fprintf(os.Stderr, "unknown collections subcommand %q\n", g.Args[0])
		os.Exit(exitUsage)
	}
}

// ----- memory verbs -----

func cmdImprint(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("imprint", flag.ExitOnError)
	text := fs.String("text", "", "memory content (use - for stdin)")
	fromFile := fs.String("from-file", "", "read content from file")
	collection := fs.String("collection", "", "collection id")
	chunkerName := fs.String("chunker", "", "chunker name (default, markdown, csv, jsonl, no-chunk)")
	enableAutoLink := fs.Bool("auto-link", false, "enable auto-link for this imprint")
	disableAutoLink := fs.Bool("no-auto-link", false, "disable auto-link for this imprint")
	tagPairs := newRepeatable()
	fs.Var(tagPairs, "tag", "k=v tag (repeatable)")
	chunkerOpts := newRepeatable()
	fs.Var(chunkerOpts, "chunker-opt", "chunker config k=v (repeatable, e.g. rows_per_cell=5)")
	_ = fs.Parse(g.Args)

	content, err := loadContent(*text, *fromFile)
	die(err)
	if content == "" {
		fmt.Fprintln(os.Stderr, "imprint: provide --text or --from-file")
		os.Exit(exitUsage)
	}
	tags := map[string]string{}
	for _, p := range tagPairs.values {
		if i := strings.Index(p, "="); i > 0 {
			tags[p[:i]] = p[i+1:]
		}
	}
	chunkerCfg := map[string]string{}
	for _, p := range chunkerOpts.values {
		i := strings.Index(p, "=")
		if i <= 0 {
			fmt.Fprintf(os.Stderr, "imprint: --chunker-opt must be key=value, got %q\n", p)
			os.Exit(exitUsage)
		}
		chunkerCfg[p[:i]] = p[i+1:]
	}
	var autoLink *bool
	if *enableAutoLink {
		v := true
		autoLink = &v
	} else if *disableAutoLink {
		v := false
		autoLink = &v
	}
	resp, err := c.Imprint(ctx, g.Workspace, api.ImprintRequest{
		CollectionID:  *collection,
		Content:       content,
		Tags:          tags,
		ChunkerID:     *chunkerName,
		ChunkerConfig: chunkerCfg,
		AutoLink:      autoLink,
	})
	die(err)
	if g.Output == "text" {
		fmt.Printf("✓ Imprinted %s in workspace %s\n", resp.ID, g.Workspace)
		fmt.Printf("  Watermark:    %s\n", resp.Watermark)
		fmt.Printf("  Cells:        %d\n", resp.CellsCreated)
		fmt.Printf("  Recall ready: %v\n", resp.RecallReady)
		fmt.Printf("  Agent:        %s\n", resp.WrittenByAgentID)
	} else {
		emit(g, resp)
	}
}

func cmdLookup(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "lookup <mem_id>")
		os.Exit(exitUsage)
	}
	env, err := c.Lookup(ctx, g.Workspace, g.Args[0])
	die(err)
	emit(g, env)
}

func cmdUpdate(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	text := fs.String("text", "", "new content")
	fromFile := fs.String("from-file", "", "load content from file")
	ifMatch := fs.String("if-match", "", "expected head watermark")
	memID := positional(parseArgs(fs, g.Args), 0)
	if memID == "" {
		fmt.Fprintln(os.Stderr, "update <mem_id> --text|--from-file ... --if-match <wmk>")
		os.Exit(exitUsage)
	}
	content, err := loadContent(*text, *fromFile)
	die(err)
	resp, err := c.Update(ctx, g.Workspace, memID, *ifMatch, api.UpdateRequest{Content: content, ExpectedWatermark: *ifMatch})
	die(err)
	emit(g, resp)
}

func cmdPatch(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("patch", flag.ExitOnError)
	patchJSON := fs.String("patch", "", "inline patch JSON: [{\"old_string\":...,\"new_string\":...}]")
	patchFile := fs.String("patch-file", "", "read patch ops from file")
	ifMatch := fs.String("if-match", "", "expected head watermark")
	memID := positional(parseArgs(fs, g.Args), 0)
	if memID == "" {
		fmt.Fprintln(os.Stderr, "patch <mem_id> --patch '[{...}]' --if-match <wmk>")
		os.Exit(exitUsage)
	}
	raw := *patchJSON
	if *patchFile != "" {
		b, err := os.ReadFile(*patchFile)
		die(err)
		raw = string(b)
	}
	var ops []api.PatchOp
	die(json.Unmarshal([]byte(raw), &ops))
	resp, err := c.Patch(ctx, g.Workspace, memID, *ifMatch, api.PatchRequest{Patch: ops, ExpectedWatermark: *ifMatch})
	die(err)
	if g.Output == "text" {
		fmt.Printf("✓ Patched %s\n", resp.ID)
		fmt.Printf("  Watermark:        %s\n", resp.Watermark)
		fmt.Printf("  Patches applied:  %d\n", resp.PatchesApplied)
		fmt.Printf("  Cells re-embed:   %d\n", resp.CellsReembed)
		fmt.Printf("  Cells skipped:    %d (the moat)\n", resp.CellsSkipped)
		if resp.CellsAdded > 0 {
			fmt.Printf("  Cells added:      %d\n", resp.CellsAdded)
		}
		if resp.CellsRemoved > 0 {
			fmt.Printf("  Cells removed:    %d\n", resp.CellsRemoved)
		}
	} else {
		emit(g, resp)
	}
}

func cmdAppend(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("append", flag.ExitOnError)
	text := fs.String("text", "", "content to append (use - for stdin)")
	ifMatch := fs.String("if-match", "", "expected head watermark")
	memID := positional(parseArgs(fs, g.Args), 0)
	if memID == "" {
		fmt.Fprintln(os.Stderr, "append <mem_id> --text ... [--if-match <wmk>]")
		os.Exit(exitUsage)
	}
	content, err := loadContent(*text, "")
	die(err)
	resp, err := c.Append(ctx, g.Workspace, memID, *ifMatch, api.AppendRequest{Content: content, ExpectedWatermark: *ifMatch})
	die(err)
	emit(g, resp)
}

func cmdForget(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "forget <mem_id>")
		os.Exit(exitUsage)
	}
	resp, err := c.Forget(ctx, g.Workspace, g.Args[0])
	die(err)
	if g.Output == "text" {
		fmt.Printf("✓ Forgot %s (cascaded %d edges)\n", resp.ID, resp.CascadedEdges)
	} else {
		emit(g, resp)
	}
}

func cmdRecall(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("recall", flag.ExitOnError)
	mode := fs.String("mode", "hybrid", "lookup|keyword|vector|hybrid")
	k := fs.Int("k", 5, "max results")
	collection := fs.String("collection", "", "collection filter")
	depth := fs.Int("neighbor-depth", 0, "graph_expansion.depth (0 = no expansion)")
	edgeTypes := fs.String("edge-types", "", "comma-separated edge_types for expansion")

	args := parseArgs(fs, g.Args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "recall <query>")
		os.Exit(exitUsage)
	}
	query := strings.Join(args, " ")
	req := api.RecallRequest{
		Query:   query,
		Mode:    api.RecallMode(*mode),
		K:       *k,
		Filters: api.RecallFilters{CollectionID: *collection},
	}
	if *depth > 0 {
		req.GraphExpansion = &api.GraphExpansion{
			Depth:     *depth,
			Direction: api.GraphDirOut,
		}
		if *edgeTypes != "" {
			req.GraphExpansion.EdgeTypes = strings.Split(*edgeTypes, ",")
		}
	}
	resp, err := c.Recall(ctx, g.Workspace, req)
	die(err)
	if g.Output == "text" {
		if len(resp.Results) == 0 {
			fmt.Println("(no results)")
			return
		}
		for i, h := range resp.Results {
			fmt.Printf("[%d] score=%.4f via=%s mem=%s\n", i+1, h.Score, h.Via, h.MemoryID)
			if h.GraphProvenance != nil {
				fmt.Printf("    via %s (edge_id=%s, from=%s, layer=%d)\n",
					h.GraphProvenance.EdgeType, h.GraphProvenance.EdgeID,
					h.GraphProvenance.FromMemoryID, h.GraphProvenance.Layer)
			}
			fmt.Printf("    %s\n", truncate(h.Text, 140))
		}
		fmt.Printf("(%d results, %d candidates, %d graph nodes, %dms)\n",
			len(resp.Results), resp.TotalCandidatesScanned, resp.GraphNodesExpanded, resp.LatencyMS)
	} else {
		emit(g, resp)
	}
}

// cmdSearch is `search <query>`: node-wide search, every workspace unless
// --workspaces narrows it. --workspace is ignored.
func cmdSearch(ctx context.Context, c *client.Client, g globalFlags) {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	mode := fs.String("mode", "hybrid", "keyword|vector|hybrid")
	k := fs.Int("k", 5, "max results")
	workspaces := fs.String("workspaces", "", "comma-separated workspace IDs to search (default: all)")
	federated := fs.Bool("federated", false, "also search this node's federation peers")
	peers := fs.String("peers", "", "with --federated: comma-separated peer node IDs (default: all)")
	perPeerK := fs.Int("per-peer-k", 0, "with --federated: hits per peer (default: --k)")
	timeoutMS := fs.Int("timeout-ms", 0, "with --federated: per-peer timeout in ms (default: each peer's)")
	includeContent := fs.Bool("include-content", false, "return each Memory's full content")
	args := parseArgs(fs, g.Args)
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "search <query> [--mode keyword|vector|hybrid] [--k N] [--workspaces id,...] [--federated [--peers id,...] [--per-peer-k N] [--timeout-ms N]] [--include-content]")
		os.Exit(exitUsage)
	}
	req := api.SearchRequest{
		Query: strings.Join(args, " "), Mode: api.RecallMode(*mode), K: *k,
		Federated: *federated, PerPeerK: *perPeerK, TimeoutMS: *timeoutMS, IncludeContent: *includeContent,
	}
	if *workspaces != "" {
		req.WorkspaceIDs = strings.Split(*workspaces, ",")
	}
	if *peers != "" {
		req.Peers = strings.Split(*peers, ",")
	}
	resp, err := c.Search(ctx, req)
	die(err)
	if g.Output != "text" {
		emit(g, resp)
		return
	}
	if len(resp.Results) == 0 {
		fmt.Println("(no results)")
	}
	for i, h := range resp.Results {
		node := ""
		if h.NodeName != "" {
			node = " node=" + h.NodeName
		}
		fmt.Printf("[%d] score=%.4f%s ws=%s mem=%s\n    %s\n", i+1, h.Score, node, h.WorkspaceID, h.MemoryID, truncate(h.Text, 140))
	}
	for _, f := range resp.FailedPeers {
		fmt.Printf("peer %s (%s) did not answer: %s\n", f.Name, f.NodeID, f.ErrorCode)
	}
	fmt.Printf("(%d results, %d candidates, %dms)\n", len(resp.Results), resp.TotalCandidatesScanned, resp.LatencyMS)
}

// cmdFetch is `fetch <memory_id> [--node <node_id>]`: a full Memory from
// the node that holds it (this node without --node).
func cmdFetch(ctx context.Context, c *client.Client, g globalFlags) {
	fs := flag.NewFlagSet("fetch", flag.ExitOnError)
	node := fs.String("node", "", "node ID of the peer that holds it (default: this node)")
	args := parseArgs(fs, g.Args)
	if len(args) != 1 {
		fmt.Fprintln(os.Stderr, "fetch <memory_id> [--node <node_id>]")
		os.Exit(exitUsage)
	}
	env, err := c.FetchMemory(ctx, args[0], *node)
	die(err)
	if g.Output != "text" {
		emit(g, env)
		return
	}
	fmt.Printf("memory:    %s\nnode:      %s (%s)\nworkspace: %s\nwatermark: %s\ncells:     %d\n\n%s\n",
		env.ID, env.NodeName, env.NodeID, env.WorkspaceID, env.HeadWatermark, len(env.Cells), env.Content)
}

func cmdList(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	collection := fs.String("collection", "", "filter by collection id")
	limit := fs.Int("limit", 50, "max results")
	_ = fs.Parse(g.Args)
	mems, err := c.ListMemories(ctx, g.Workspace, *collection, *limit)
	die(err)
	if g.Output == "text" {
		for _, m := range mems {
			fmt.Printf("%s  %s\n", m.ID, truncate(m.Content, 80))
		}
	} else {
		emit(g, map[string]any{"memories": mems})
	}
}

func cmdWatermarks(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "watermarks <mem_id>")
		os.Exit(exitUsage)
	}
	hist, err := c.WatermarkHistory(ctx, g.Workspace, g.Args[0])
	die(err)
	emit(g, map[string]any{"watermarks": hist})
}

// ----- Context Graph -----

func cmdLink(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	edgeType := fs.String("type", "references", "parent_of|derived_from|supersedes|references|session_of|mentions")
	props := fs.String("properties", "", "optional JSON object of free-form properties")
	pos := parseArgs(fs, g.Args)
	if len(pos) < 2 {
		fmt.Fprintln(os.Stderr, "link <source_mem_id> <target_mem_id> --type <edge_type>")
		os.Exit(exitUsage)
	}
	var p map[string]any
	if *props != "" {
		die(json.Unmarshal([]byte(*props), &p))
	}
	resp, err := c.Link(ctx, g.Workspace, pos[0], api.LinkRequest{
		TargetMemoryID: pos[1],
		EdgeType:       *edgeType,
		Properties:     p,
	})
	die(err)
	if g.Output == "text" {
		fmt.Printf("✓ Linked %s --%s--> %s (edge=%s)\n", pos[0], *edgeType, pos[1], resp.Edge.EdgeID)
	} else {
		emit(g, resp)
	}
}

func cmdUnlink(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "unlink <edge_id>")
		os.Exit(exitUsage)
	}
	die(c.Unlink(ctx, g.Workspace, g.Args[0]))
	fmt.Println("✓ unlinked", g.Args[0])
}

func cmdNeighbors(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("neighbors", flag.ExitOnError)
	direction := fs.String("direction", "both", "out|in|both")
	edgeTypes := fs.String("types", "", "comma-separated edge_types")
	k := fs.Int("k", 50, "max neighbors")
	memID := positional(parseArgs(fs, g.Args), 0)
	if memID == "" {
		fmt.Fprintln(os.Stderr, "neighbors <mem_id>")
		os.Exit(exitUsage)
	}
	req := api.NeighborsRequest{
		MemoryID:  memID,
		Direction: api.GraphDirection(*direction),
		K:         *k,
	}
	if *edgeTypes != "" {
		req.EdgeTypes = strings.Split(*edgeTypes, ",")
	}
	resp, err := c.Neighbors(ctx, g.Workspace, req)
	die(err)
	emit(g, resp)
}

func cmdTraverse(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	fs := flag.NewFlagSet("traverse", flag.ExitOnError)
	depth := fs.Int("depth", 1, "BFS depth (max 3 in OSS)")
	direction := fs.String("direction", "out", "out|in|both")
	edgeTypes := fs.String("types", "", "comma-separated edge_types")
	seedID := positional(parseArgs(fs, g.Args), 0)
	if seedID == "" {
		fmt.Fprintln(os.Stderr, "traverse <seed_mem_id>")
		os.Exit(exitUsage)
	}
	req := api.TraverseRequest{
		SeedMemoryID: seedID,
		Depth:        *depth,
		Direction:    api.GraphDirection(*direction),
	}
	if *edgeTypes != "" {
		req.EdgeTypes = strings.Split(*edgeTypes, ",")
	}
	resp, err := c.Traverse(ctx, g.Workspace, req)
	die(err)
	emit(g, resp)
}

func cmdGraph(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 || g.Args[0] != "stats" {
		fmt.Fprintln(os.Stderr, "graph stats")
		os.Exit(exitUsage)
	}
	resp, err := c.GraphStats(ctx, g.Workspace)
	die(err)
	emit(g, resp)
}

// ----- Agents -----

func cmdAgents(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "agents <list|register>")
		os.Exit(exitUsage)
	}
	switch g.Args[0] {
	case "list":
		ags, err := c.ListAgents(ctx, g.Workspace)
		die(err)
		emit(g, map[string]any{"agents": ags})
	case "register":
		// --agent-id is a global flag (parseGlobal consumes it wherever it
		// appears), so the agent registered is the global one: the one
		// --agent-id names, or else the agent this CLI writes as (#6175).
		fs := flag.NewFlagSet("agents register", flag.ExitOnError)
		id := fs.String("id", "", "agent id (default: --agent-id, $MEMORA_AGENT_ID or the shared CLI agent)")
		provider := fs.String("provider", "opaque", "identity provider")
		name := fs.String("display-name", "", "display name")
		_ = fs.Parse(g.Args[1:])
		if *id == "" {
			*id = g.AgentID
		}
		if *id == "" {
			fmt.Fprintln(os.Stderr, "agents register [--agent-id <uuid>] [--display-name <name>]")
			os.Exit(exitUsage)
		}
		a, err := c.RegisterAgent(ctx, g.Workspace, api.RegisterAgentRequest{
			AgentID: *id, IdentityProvider: *provider, DisplayName: *name,
		})
		die(err)
		emit(g, a)
	default:
		fmt.Fprintf(os.Stderr, "unknown agents subcommand %q\n", g.Args[0])
		os.Exit(exitUsage)
	}
}

// ----- Pins -----

func cmdPin(ctx context.Context, c *client.Client, g globalFlags) {
	requireWorkspace(g)
	if len(g.Args) == 0 {
		fmt.Fprintln(os.Stderr, "pin <create|list|delete>")
		os.Exit(exitUsage)
	}
	switch g.Args[0] {
	case "create":
		fs := flag.NewFlagSet("pin create", flag.ExitOnError)
		query := fs.String("query", "", "recall query text (required)")
		mode := fs.String("mode", "hybrid", "recall mode")
		k := fs.Int("k", 10, "top-k results")
		watermark := fs.String("watermark", "", "watermark to bind")
		label := fs.String("label", "", "optional label")
		_ = fs.Parse(g.Args[1:])
		if *query == "" {
			fmt.Fprintln(os.Stderr, "pin create --query <text>")
			os.Exit(exitUsage)
		}
		resp, err := c.CreatePin(ctx, g.Workspace, api.PinRequest{
			Query: *query, Mode: *mode, K: *k, Watermark: *watermark, Label: *label,
		})
		die(err)
		emit(g, resp)
	case "list":
		pins, err := c.ListPins(ctx, g.Workspace)
		die(err)
		emit(g, map[string]any{"pins": pins})
	case "delete":
		if len(g.Args) < 2 {
			fmt.Fprintln(os.Stderr, "pin delete <pin_id>")
			os.Exit(exitUsage)
		}
		die(c.DeletePin(ctx, g.Workspace, g.Args[1]))
		emit(g, map[string]string{"status": "deleted", "pin_id": g.Args[1]})
	default:
		fmt.Fprintf(os.Stderr, "unknown pin subcommand %q\n", g.Args[0])
		os.Exit(exitUsage)
	}
}

// ----- Health -----

func cmdHealth(ctx context.Context, c *client.Client, g globalFlags) {
	resp, err := c.Health(ctx)
	die(err)
	emit(g, resp)
}

func cmdReady(ctx context.Context, c *client.Client, g globalFlags) {
	resp, err := c.Ready(ctx)
	die(err)
	emit(g, resp)
}

// ----- migrate -----

func cmdMigrate(ctx context.Context, g globalFlags) {
	if len(g.Args) == 0 || g.Args[0] != "content" {
		fmt.Fprintln(os.Stderr, "usage: memora-cli migrate content [flags]")
		os.Exit(exitUsage)
	}
	fs := flag.NewFlagSet("migrate content", flag.ExitOnError)
	workspace := fs.String("workspace", g.Workspace, "workspace ID")
	collection := fs.String("collection", "", "optional collection filter")
	dataDir := fs.String("data-dir", getenv("MEMORA_DATA_DIR", "./data"), "data directory for SQLite")
	metadataDriver := fs.String("metadata-driver", getenv("MEMORA_METADATA_DRIVER", "sqlite"), "metadata-store driver")
	contentDriver := fs.String("content-driver", getenv("MEMORA_CONTENT_DRIVER", "sqlite"), "content-store driver")
	contentDSN := fs.String("content-dsn", os.Getenv("MEMORA_CONTENT_DSN"), "content-store DSN")
	resumeFrom := fs.String("resume-from", "", "memory ID to resume from")
	dryRun := fs.Bool("dry-run", false, "report counts without writing")
	verify := fs.Bool("verify", false, "verify content matches between metadata and content store")
	maxRate := fs.Int("max-rate", 100, "max memories per second (0 = unlimited)")
	_ = fs.Parse(g.Args[1:])

	if *workspace == "" {
		fmt.Fprintln(os.Stderr, "migrate content: --workspace required")
		os.Exit(exitUsage)
	}

	dbPath := *dataDir + "/memora.db"
	primary, err := adapter.OpenMetadata(ctx, adapter.MetadataConfig{Driver: *metadataDriver, DSN: dbPath})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open metadata: %v\n", err)
		os.Exit(exitClient)
	}
	defer primary.Close()

	cdsn := *contentDSN
	if cdsn == "" {
		if *contentDriver == "sqlite" {
			cdsn = dbPath
		} else {
			cdsn = *dataDir + "/content"
		}
	}
	content, err := adapter.OpenContent(ctx, adapter.ContentConfig{Driver: *contentDriver, DSN: cdsn})
	if err != nil {
		fmt.Fprintf(os.Stderr, "open content: %v\n", err)
		os.Exit(exitClient)
	}
	defer content.Close()

	result, err := mcli.MigrateContent(ctx, primary, content, mcli.MigrateContentConfig{
		WorkspaceID:  *workspace,
		CollectionID: *collection,
		ResumeFrom:   *resumeFrom,
		DryRun:       *dryRun,
		Verify:       *verify,
		MaxRate:      *maxRate,
		Out:          os.Stdout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "migrate: %v\n", err)
		os.Exit(exitClient)
	}

	b, _ := json.Marshal(result)
	fmt.Println(string(b))

	if result.Mismatches > 0 {
		os.Exit(exitClient)
	}
}

// ----- helpers -----

func requireWorkspace(g globalFlags) {
	if g.Workspace == "" {
		fmt.Fprintln(os.Stderr, "error: --workspace required (or set MEMORA_WORKSPACE)")
		os.Exit(exitUsage)
	}
}

func loadContent(text, file string) (string, error) {
	if text == "-" {
		b, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	if file != "" {
		b, err := os.ReadFile(file)
		if err != nil {
			return "", err
		}
		return string(b), nil
	}
	return text, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// parseArgs parses args into fs and returns the positional arguments in
// order. Unlike fs.Parse, which stops at the first positional, flags may
// come before, between or after positionals, as the usage lines show. A
// literal "--" ends flag parsing; everything after it is positional, so a
// query can contain text that starts with "-".
func parseArgs(fs *flag.FlagSet, args []string) []string {
	var tail []string
	for i, a := range args {
		if a == "--" {
			args, tail = args[:i], args[i+1:]
			break
		}
	}
	var pos []string
	for {
		_ = fs.Parse(args) // ExitOnError: a bad flag exits with usage
		rest := fs.Args()
		if len(rest) == 0 {
			return append(pos, tail...)
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

// positional returns pos[i], or "" when there are fewer positionals.
func positional(pos []string, i int) string {
	if i < len(pos) {
		return pos[i]
	}
	return ""
}

type repeatable struct{ values []string }

func newRepeatable() *repeatable { return &repeatable{} }

func (r *repeatable) String() string     { return strings.Join(r.values, ",") }
func (r *repeatable) Set(v string) error { r.values = append(r.values, v); return nil }

// ----- help -----

func printRootHelp() {
	fmt.Println(`memora-cli — command-line client for Memora.

Usage:
  memora-cli [global-flags] <command> [args...]

Commands:
  workspaces list|create|show|delete
  collections list|create
  imprint --text|--from-file [--tag k=v ...] [--chunker csv|jsonl|markdown|...]
          [--chunker-opt key=value ...]
  lookup <mem_id>
  update <mem_id> --text|--from-file --if-match <wmk>
  patch <mem_id> --patch '[{...}]'|--patch-file <path> --if-match <wmk>
  append <mem_id> --text|--from-file [--if-match <wmk>]
  forget <mem_id>
  recall <query> [--mode hybrid|keyword|vector|lookup] [--k N]
                 [--neighbor-depth N --edge-types references,...]
  list [--collection <id>] [--limit N]
  watermarks <mem_id>
  link <src_mem_id> <tgt_mem_id> --type references|...
  search <query> [--mode keyword|vector|hybrid] [--k N] [--workspaces id,...]
         [--federated [--peers id,...] [--per-peer-k N] [--timeout-ms N]] [--include-content]
  fetch <memory_id> [--node <node_id>]
  unlink <edge_id>
  neighbors <mem_id> [--direction out|in|both] [--types ...] [--k N]
  traverse <seed_mem_id> [--depth 1..3] [--direction ...] [--types ...]
  graph stats
  agents list|register [--agent-id <uuid>] [--display-name <name>]
  health
  ready
  migrate content [--workspace ...] [--dry-run|--verify] [--resume-from ...]
  version

Global flags:
  --endpoint <url>       (default: $MEMORA_ENDPOINT or http://localhost:7777)
  --api-key <key>        (default: $MEMORA_API_KEY)
  --agent-id <uuid>      (default: $MEMORA_AGENT_ID or the shared local-CLI agent
                         ` + defaultAgentID + `)
  --workspace, -w <id>   (default: $MEMORA_WORKSPACE)
  --output, -o text|json|jsonl|yaml
  --timeout <duration>   (default: 30s)
  --no-color             disable colored output
  --quiet, -q            suppress normal output
  --verbose, -v          enable verbose/debug output`)
}
