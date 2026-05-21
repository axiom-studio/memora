package ui

import (
	"encoding/json"
	"fmt"
	"html/template"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handler) handleGraphData(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	seedMemID := strings.TrimSpace(r.URL.Query().Get("seed"))
	depthStr := r.URL.Query().Get("depth")

	if h.data == nil || wsID == "" {
		http.Error(w, `{"error":"missing workspace"}`, http.StatusBadRequest)
		return
	}

	depth, _ := strconv.Atoi(depthStr)
	if depth <= 0 {
		depth = 2
	}

	data, err := h.data.GraphData(r.Context(), wsID, seedMemID, depth)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleGraphNeighbors(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	memID := strings.TrimSpace(r.URL.Query().Get("memory_id"))
	direction := r.URL.Query().Get("direction")
	edgeTypesStr := strings.TrimSpace(r.URL.Query().Get("edge_types"))
	kStr := r.URL.Query().Get("k")

	if h.data == nil || wsID == "" || memID == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing workspace or memory_id"}`, http.StatusBadRequest)
		return
	}

	k, _ := strconv.Atoi(kStr)
	var edgeTypes []string
	if edgeTypesStr != "" {
		edgeTypes = strings.Split(edgeTypesStr, ",")
	}

	data, err := h.data.GraphNeighbors(r.Context(), wsID, memID, direction, edgeTypes, k)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleGraphTraverse(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	seedMemID := strings.TrimSpace(r.URL.Query().Get("seed"))
	direction := r.URL.Query().Get("direction")
	edgeTypesStr := strings.TrimSpace(r.URL.Query().Get("edge_types"))
	depthStr := r.URL.Query().Get("depth")

	if h.data == nil || wsID == "" || seedMemID == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing workspace or seed"}`, http.StatusBadRequest)
		return
	}

	depth, _ := strconv.Atoi(depthStr)
	var edgeTypes []string
	if edgeTypesStr != "" {
		edgeTypes = strings.Split(edgeTypesStr, ",")
	}

	data, err := h.data.GraphTraverse(r.Context(), wsID, seedMemID, direction, edgeTypes, depth)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(data)
}

func (h *Handler) handleGraphStats(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	if h.data == nil || wsID == "" {
		w.Header().Set("Content-Type", "application/json")
		http.Error(w, `{"error":"missing workspace"}`, http.StatusBadRequest)
		return
	}

	nodeCount, edgeCountByType, err := h.data.GraphStats(r.Context(), wsID)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"node_count":        nodeCount,
		"edge_count_by_type": edgeCountByType,
	})
}

func (h *Handler) partialGraphView(w http.ResponseWriter, r *http.Request) {
	wsID := r.URL.Query().Get("ws")
	w.Header().Set("Content-Type", "text/html; charset=utf-8")

	if wsID == "" {
		fmt.Fprint(w, `<div class="empty-state"><p>No workspace selected.</p></div>`)
		return
	}

	edgeTypeChecks := ""
	for _, et := range []string{"parent_of", "derived_from", "supersedes", "references", "session_of", "mentions", "vector_neighbor"} {
		edgeTypeChecks += fmt.Sprintf(`<label style="display:inline-flex;align-items:center;gap:0.3rem"><input type="checkbox" class="edge-type-filter" value="%s" checked> %s</label> `, et, et)
	}

	fmt.Fprintf(w, `<div class="card mb-2">
<h3 class="card-title">Context Graph</h3>
<div class="tabs" style="margin-bottom:0.75rem">
  <a class="tab active" onclick="showGraphTab('overview',this)">Overview</a>
  <a class="tab" onclick="showGraphTab('neighbors',this)">Neighbors</a>
  <a class="tab" onclick="showGraphTab('traverse',this)">Traverse</a>
</div>

<div id="graph-tab-overview">
  <div style="display:flex;gap:0.75rem;align-items:end;margin-bottom:0.75rem;flex-wrap:wrap">
    <label>Seed Memory <span class="text-muted">(optional)</span>
      <input type="text" id="graph-seed" placeholder="mem_... (leave empty for full graph)" list="graph-seed-suggest"
        hx-get="/ui/partials/memory-suggest?ws=%s" hx-trigger="keyup changed delay:250ms" hx-target="#graph-seed-suggest" hx-swap="innerHTML" name="q">
      <datalist id="graph-seed-suggest"></datalist>
    </label>
    <label>Depth
      <input type="number" id="graph-depth" value="2" min="1" max="5" style="width:70px">
    </label>
    <button class="btn btn-primary" onclick="loadGraph()">Load Graph</button>
  </div>
</div>

<div id="graph-tab-neighbors" style="display:none">
  <div style="display:flex;gap:0.75rem;align-items:end;margin-bottom:0.75rem;flex-wrap:wrap">
    <label>Memory ID <span style="color:var(--danger)">*</span>
      <input type="text" id="nb-memory-id" placeholder="mem_..." required list="nb-mem-suggest"
        hx-get="/ui/partials/memory-suggest?ws=%s" hx-trigger="keyup changed delay:250ms" hx-target="#nb-mem-suggest" hx-swap="innerHTML" name="q">
      <datalist id="nb-mem-suggest"></datalist>
    </label>
    <label>Direction
      <select id="nb-direction"><option value="both">both</option><option value="out">out</option><option value="in">in</option></select>
    </label>
    <label>K
      <input type="number" id="nb-k" value="50" min="1" max="200" style="width:70px">
    </label>
    <button class="btn btn-primary" onclick="loadNeighbors()">Query Neighbors</button>
  </div>
  <div style="margin-bottom:0.75rem"><label style="font-size:0.85rem;font-weight:600">Edge Types</label><div id="nb-edge-types" style="display:flex;gap:0.75rem;flex-wrap:wrap;margin-top:0.25rem">%s</div></div>
</div>

<div id="graph-tab-traverse" style="display:none">
  <div style="display:flex;gap:0.75rem;align-items:end;margin-bottom:0.75rem;flex-wrap:wrap">
    <label>Seed Memory <span style="color:var(--danger)">*</span>
      <input type="text" id="tr-seed" placeholder="mem_..." required list="tr-seed-suggest"
        hx-get="/ui/partials/memory-suggest?ws=%s" hx-trigger="keyup changed delay:250ms" hx-target="#tr-seed-suggest" hx-swap="innerHTML" name="q">
      <datalist id="tr-seed-suggest"></datalist>
    </label>
    <label>Depth
      <input type="number" id="tr-depth" value="2" min="1" max="5" style="width:70px">
    </label>
    <label>Direction
      <select id="tr-direction"><option value="both">both</option><option value="out">out</option><option value="in">in</option></select>
    </label>
    <button class="btn btn-primary" onclick="loadTraverse()">Run Traverse</button>
  </div>
  <div style="margin-bottom:0.75rem"><label style="font-size:0.85rem;font-weight:600">Edge Types</label><div id="tr-edge-types" style="display:flex;gap:0.75rem;flex-wrap:wrap;margin-top:0.25rem">%s</div></div>
</div>

<div id="graph-stats" class="text-muted mb-1"></div>
<div id="graph-container" style="width:100%%;height:600px;border:1px solid var(--border);border-radius:8px;background:var(--bg-secondary);position:relative;overflow:hidden">
  <div id="graph-empty" style="display:flex;align-items:center;justify-content:center;height:100%%">
    <p class="text-muted">Select a query mode and click the button to visualize the graph.</p>
  </div>
  <canvas id="graph-canvas" style="display:none;width:100%%;height:100%%"></canvas>
</div>
<div id="graph-tooltip" style="display:none;position:fixed;background:var(--bg);border:1px solid var(--border);border-radius:4px;padding:0.5rem;font-size:0.85rem;box-shadow:0 2px 8px rgba(0,0,0,0.15);z-index:1000;max-width:300px"></div>
</div>`,
		template.HTMLEscapeString(wsID),
		template.HTMLEscapeString(wsID), edgeTypeChecks,
		template.HTMLEscapeString(wsID), edgeTypeChecks)

	fmt.Fprintf(w, `<script>
var wsID = %q;
`, template.HTMLEscapeString(wsID))

	fmt.Fprint(w, `
function showGraphTab(name, el) {
  ['overview','neighbors','traverse'].forEach(function(t) {
    document.getElementById('graph-tab-'+t).style.display = t===name ? 'block' : 'none';
  });
  el.parentElement.querySelectorAll('.tab').forEach(function(a) { a.classList.remove('active'); });
  el.classList.add('active');
}

function getCheckedEdgeTypes(containerId) {
  var checks = document.querySelectorAll('#' + containerId + ' .edge-type-filter:checked');
  var types = [];
  checks.forEach(function(c) { types.push(c.value); });
  return types;
}

function displayGraphResult(data) {
  if (data.error) {
    document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Error: ' + escapeHtml(data.error) + '</span>';
    return;
  }
  var parts = [data.node_count + ' nodes'];
  if (data.edge_count_by_type) {
    var total = 0;
    for (var t in data.edge_count_by_type) total += data.edge_count_by_type[t];
    parts.push(total + ' edges');
    var types = [];
    for (var t in data.edge_count_by_type) types.push(t + ': ' + data.edge_count_by_type[t]);
    if (types.length) parts.push('(' + types.join(', ') + ')');
  }
  var rendered = (data.nodes || []).length + ' nodes, ' + (data.edges || []).length + ' edges rendered';
  document.getElementById('graph-stats').textContent = parts.join(' · ') + ' | ' + rendered;
  renderGraph(data.nodes || [], data.edges || []);
}

function loadGraph() {
  var seed = document.getElementById('graph-seed').value.trim();
  var depth = document.getElementById('graph-depth').value;
  var url = '/ui/api/graph/data?ws=' + encodeURIComponent(wsID) + '&depth=' + depth;
  if (seed) url += '&seed=' + encodeURIComponent(seed);
  fetch(url).then(function(r) { return r.json(); }).then(displayGraphResult).catch(function() {
    document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Fetch failed</span>';
  });
}

function loadNeighbors() {
  var memID = document.getElementById('nb-memory-id').value.trim();
  if (!memID) { document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Memory ID is required</span>'; return; }
  var dir = document.getElementById('nb-direction').value;
  var k = document.getElementById('nb-k').value;
  var types = getCheckedEdgeTypes('nb-edge-types');
  var url = '/ui/api/graph/neighbors?ws=' + encodeURIComponent(wsID) + '&memory_id=' + encodeURIComponent(memID) + '&direction=' + dir + '&k=' + k;
  if (types.length < 7) url += '&edge_types=' + types.join(',');
  fetch(url).then(function(r) { return r.json(); }).then(displayGraphResult).catch(function() {
    document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Fetch failed</span>';
  });
}

function loadTraverse() {
  var seed = document.getElementById('tr-seed').value.trim();
  if (!seed) { document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Seed memory is required</span>'; return; }
  var depth = document.getElementById('tr-depth').value;
  var dir = document.getElementById('tr-direction').value;
  var types = getCheckedEdgeTypes('tr-edge-types');
  var url = '/ui/api/graph/traverse?ws=' + encodeURIComponent(wsID) + '&seed=' + encodeURIComponent(seed) + '&depth=' + depth + '&direction=' + dir;
  if (types.length < 7) url += '&edge_types=' + types.join(',');
  fetch(url).then(function(r) { return r.json(); }).then(displayGraphResult).catch(function() {
    document.getElementById('graph-stats').innerHTML = '<span style="color:var(--danger)">Fetch failed</span>';
  });
}

var EDGE_COLORS = {
  'related_to': '#6366f1',
  'derived_from': '#f59e0b',
  'supersedes': '#ef4444',
  'references': '#10b981',
  'part_of': '#8b5cf6',
  'vector_neighbor': '#94a3b8'
};

function edgeColor(type) {
  return EDGE_COLORS[type] || '#64748b';
}

function renderGraph(nodes, edges) {
  var container = document.getElementById('graph-container');
  var canvas = document.getElementById('graph-canvas');
  var empty = document.getElementById('graph-empty');

  if (!nodes.length) {
    empty.style.display = 'flex';
    canvas.style.display = 'none';
    empty.innerHTML = '<p class="text-muted">No graph data. Try a different seed or check that edges exist.</p>';
    return;
  }

  empty.style.display = 'none';
  canvas.style.display = 'block';
  canvas.width = container.clientWidth;
  canvas.height = container.clientHeight;

  var adj = {};
  nodes.forEach(function(n) { adj[n.id] = []; });
  edges.forEach(function(e) {
    if (adj[e.source]) adj[e.source].push(e.target);
    if (adj[e.target]) adj[e.target].push(e.source);
  });

  var pos = layoutForceDirected(nodes, edges, canvas.width, canvas.height);
  var state = { pos: pos, nodes: nodes, edges: edges, zoom: 1, panX: 0, panY: 0, drag: null, hover: null };

  var ctx = canvas.getContext('2d');
  draw(ctx, state);

  canvas.onmousedown = function(ev) {
    var p = canvasCoords(ev, canvas, state);
    var hit = findNode(state, p.x, p.y);
    if (hit) {
      state.drag = { nodeIdx: hit, offX: state.pos[hit].x - p.x, offY: state.pos[hit].y - p.y };
    } else {
      state.drag = { pan: true, startX: ev.clientX, startY: ev.clientY, sPanX: state.panX, sPanY: state.panY };
    }
  };
  canvas.onmousemove = function(ev) {
    var p = canvasCoords(ev, canvas, state);
    if (state.drag && state.drag.nodeIdx !== undefined) {
      state.pos[state.drag.nodeIdx].x = p.x + state.drag.offX;
      state.pos[state.drag.nodeIdx].y = p.y + state.drag.offY;
      draw(ctx, state);
    } else if (state.drag && state.drag.pan) {
      state.panX = state.drag.sPanX + (ev.clientX - state.drag.startX);
      state.panY = state.drag.sPanY + (ev.clientY - state.drag.startY);
      draw(ctx, state);
    } else {
      var hit = findNode(state, p.x, p.y);
      state.hover = hit;
      canvas.style.cursor = hit !== null ? 'pointer' : 'grab';
      var tooltip = document.getElementById('graph-tooltip');
      if (hit !== null) {
        var n = state.nodes[hit];
        tooltip.innerHTML = '<strong>' + escapeHtml(n.id) + '</strong><br>Type: ' + escapeHtml(n.type);
        tooltip.style.display = 'block';
        tooltip.style.left = (ev.clientX + 12) + 'px';
        tooltip.style.top = (ev.clientY + 12) + 'px';
      } else {
        var edgeHit = findEdge(state, p.x, p.y);
        if (edgeHit !== null) {
          var e = state.edges[edgeHit];
          tooltip.innerHTML = '<strong>' + escapeHtml(e.label) + '</strong><br>' + escapeHtml(e.source) + ' → ' + escapeHtml(e.target);
          tooltip.style.display = 'block';
          tooltip.style.left = (ev.clientX + 12) + 'px';
          tooltip.style.top = (ev.clientY + 12) + 'px';
        } else {
          tooltip.style.display = 'none';
        }
      }
    }
  };
  canvas.onmouseup = function(ev) { state.drag = null; };
  canvas.onmouseleave = function() {
    state.drag = null;
    document.getElementById('graph-tooltip').style.display = 'none';
  };
  canvas.ondblclick = function(ev) {
    var p = canvasCoords(ev, canvas, state);
    var hit = findNode(state, p.x, p.y);
    if (hit !== null) {
      window.location.href = '/ui/workspaces/' + encodeURIComponent(wsID) + '/memories/' + encodeURIComponent(state.nodes[hit].id);
    }
  };
  canvas.onwheel = function(ev) {
    ev.preventDefault();
    var factor = ev.deltaY > 0 ? 0.9 : 1.1;
    var rect = canvas.getBoundingClientRect();
    var mx = ev.clientX - rect.left;
    var my = ev.clientY - rect.top;
    state.panX = mx - (mx - state.panX) * factor;
    state.panY = my - (my - state.panY) * factor;
    state.zoom *= factor;
    draw(ctx, state);
  };
}

function canvasCoords(ev, canvas, state) {
  var rect = canvas.getBoundingClientRect();
  return {
    x: (ev.clientX - rect.left - state.panX) / state.zoom,
    y: (ev.clientY - rect.top - state.panY) / state.zoom
  };
}

function findNode(state, x, y) {
  var r = 20 / state.zoom;
  for (var i = state.nodes.length - 1; i >= 0; i--) {
    var dx = state.pos[i].x - x, dy = state.pos[i].y - y;
    if (dx * dx + dy * dy < r * r) return i;
  }
  return null;
}

function findEdge(state, x, y) {
  var nodeIdx = {};
  state.nodes.forEach(function(n, i) { nodeIdx[n.id] = i; });
  for (var i = 0; i < state.edges.length; i++) {
    var e = state.edges[i];
    var si = nodeIdx[e.source], ti = nodeIdx[e.target];
    if (si === undefined || ti === undefined) continue;
    var sx = state.pos[si].x, sy = state.pos[si].y;
    var tx = state.pos[ti].x, ty = state.pos[ti].y;
    var dx = tx - sx, dy = ty - sy;
    var len = Math.sqrt(dx * dx + dy * dy);
    if (len < 1) continue;
    var t = ((x - sx) * dx + (y - sy) * dy) / (len * len);
    t = Math.max(0, Math.min(1, t));
    var px = sx + t * dx, py = sy + t * dy;
    var dist = Math.sqrt((x - px) * (x - px) + (y - py) * (y - py));
    if (dist < 6 / state.zoom) return i;
  }
  return null;
}

function draw(ctx, state) {
  var w = ctx.canvas.width, h = ctx.canvas.height;
  ctx.clearRect(0, 0, w, h);
  ctx.save();
  ctx.translate(state.panX, state.panY);
  ctx.scale(state.zoom, state.zoom);

  var nodeIdx = {};
  state.nodes.forEach(function(n, i) { nodeIdx[n.id] = i; });

  state.edges.forEach(function(e) {
    var si = nodeIdx[e.source], ti = nodeIdx[e.target];
    if (si === undefined || ti === undefined) return;
    var sx = state.pos[si].x, sy = state.pos[si].y;
    var tx = state.pos[ti].x, ty = state.pos[ti].y;
    ctx.beginPath();
    ctx.moveTo(sx, sy);
    ctx.lineTo(tx, ty);
    ctx.strokeStyle = edgeColor(e.label);
    ctx.lineWidth = 1.5;
    ctx.stroke();

    var mx = (sx + tx) / 2, my = (sy + ty) / 2;
    ctx.font = '10px monospace';
    ctx.fillStyle = edgeColor(e.label);
    ctx.textAlign = 'center';
    ctx.fillText(e.label, mx, my - 4);

    var angle = Math.atan2(ty - sy, tx - sx);
    var r = 14;
    var ax = tx - r * Math.cos(angle), ay = ty - r * Math.sin(angle);
    ctx.beginPath();
    ctx.moveTo(ax, ay);
    ctx.lineTo(ax - 8 * Math.cos(angle - 0.3), ay - 8 * Math.sin(angle - 0.3));
    ctx.lineTo(ax - 8 * Math.cos(angle + 0.3), ay - 8 * Math.sin(angle + 0.3));
    ctx.closePath();
    ctx.fillStyle = edgeColor(e.label);
    ctx.fill();
  });

  state.nodes.forEach(function(n, i) {
    var x = state.pos[i].x, y = state.pos[i].y;
    ctx.beginPath();
    ctx.arc(x, y, 12, 0, 2 * Math.PI);
    ctx.fillStyle = n.type === 'seed' ? '#6366f1' : '#334155';
    ctx.fill();
    ctx.strokeStyle = state.hover === i ? '#f59e0b' : '#94a3b8';
    ctx.lineWidth = state.hover === i ? 3 : 1.5;
    ctx.stroke();
    ctx.font = '11px monospace';
    ctx.fillStyle = '#e2e8f0';
    ctx.textAlign = 'center';
    ctx.fillText(n.label, x, y + 24);
  });

  ctx.restore();
}

function layoutForceDirected(nodes, edges, width, height) {
  var pos = nodes.map(function(_, i) {
    var angle = (2 * Math.PI * i) / nodes.length;
    var r = Math.min(width, height) * 0.35;
    return { x: width / 2 + r * Math.cos(angle), y: height / 2 + r * Math.sin(angle) };
  });

  var nodeIdx = {};
  nodes.forEach(function(n, i) { nodeIdx[n.id] = i; });

  for (var iter = 0; iter < 100; iter++) {
    var fx = new Array(nodes.length).fill(0);
    var fy = new Array(nodes.length).fill(0);

    for (var i = 0; i < nodes.length; i++) {
      for (var j = i + 1; j < nodes.length; j++) {
        var dx = pos[j].x - pos[i].x, dy = pos[j].y - pos[i].y;
        var dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
        var force = 5000 / (dist * dist);
        fx[i] -= force * dx / dist;
        fy[i] -= force * dy / dist;
        fx[j] += force * dx / dist;
        fy[j] += force * dy / dist;
      }
    }

    edges.forEach(function(e) {
      var si = nodeIdx[e.source], ti = nodeIdx[e.target];
      if (si === undefined || ti === undefined) return;
      var dx = pos[ti].x - pos[si].x, dy = pos[ti].y - pos[si].y;
      var dist = Math.max(Math.sqrt(dx * dx + dy * dy), 1);
      var force = (dist - 120) * 0.05;
      fx[si] += force * dx / dist;
      fy[si] += force * dy / dist;
      fx[ti] -= force * dx / dist;
      fy[ti] -= force * dy / dist;
    });

    var damping = 0.8 * (1 - iter / 100);
    for (var i = 0; i < nodes.length; i++) {
      pos[i].x += fx[i] * damping;
      pos[i].y += fy[i] * damping;
      pos[i].x = Math.max(40, Math.min(width - 40, pos[i].x));
      pos[i].y = Math.max(40, Math.min(height - 40, pos[i].y));
    }
  }
  return pos;
}

function escapeHtml(s) {
  var d = document.createElement('div');
  d.textContent = s;
  return d.innerHTML;
}
</script>`)
}
