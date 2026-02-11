package main

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/decred/dcrd/dcrutil/v4"
	"github.com/decred/dcrlnd/lnrpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

type macaroonCred struct{ value string }

func (m macaroonCred) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	return map[string]string{"macaroon": m.value}, nil
}
func (m macaroonCred) RequireTransportSecurity() bool { return true }

type graphNode struct {
	PubKey string `json:"pub_key"`
	Alias  string `json:"alias"`
}

type graphEdge struct {
	ChannelID uint64 `json:"channel_id"`
	Node1Pub  string `json:"node1_pub"`
	Node2Pub  string `json:"node2_pub"`
	Capacity  int64  `json:"capacity"`
}

type graphStats struct {
	Nodes       int    `json:"nodes"`
	Channels    int    `json:"channels"`
	TotalDCR    string `json:"total_dcr"`
	Selected    int    `json:"selected_nodes"`
	SelectedChs int    `json:"selected_channels"`
}

type graphResp struct {
	Nodes []graphNode `json:"nodes"`
	Edges []graphEdge `json:"edges"`
	Stats graphStats  `json:"stats"`
}

type app struct {
	ln            lnrpc.LightningClient
	mock          bool
	maxDefault    int
	cacheDuration time.Duration

	cacheAt time.Time
	cache   *lnrpc.ChannelGraph
}

func (a *app) describeGraph(ctx context.Context) (*lnrpc.ChannelGraph, error) {
	if a.mock {
		return mockGraph(), nil
	}
	if a.cache != nil && time.Since(a.cacheAt) < a.cacheDuration {
		return a.cache, nil
	}
	g, err := a.ln.DescribeGraph(ctx, &lnrpc.ChannelGraphRequest{IncludeUnannounced: false})
	if err != nil {
		return nil, err
	}
	a.cacheAt = time.Now()
	a.cache = g
	return g, nil
}

func (a *app) graphHandler(w http.ResponseWriter, r *http.Request) {
	maxNodes := a.maxDefault
	if p := r.URL.Query().Get("max_nodes"); p != "" {
		var v int
		if _, err := fmt.Sscanf(p, "%d", &v); err == nil && v > 10 {
			maxNodes = v
		}
	}

	graph, err := a.describeGraph(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}

	resp := toGraphResp(graph, maxNodes)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func toGraphResp(g *lnrpc.ChannelGraph, maxNodes int) graphResp {
	degree := make(map[string]int, len(g.Nodes))
	var totalCap int64
	for _, e := range g.Edges {
		degree[e.Node1Pub]++
		degree[e.Node2Pub]++
		totalCap += e.Capacity
	}
	selectedKeys := make(map[string]struct{}, maxNodes)
	if len(g.Nodes) <= maxNodes {
		for _, n := range g.Nodes {
			selectedKeys[n.PubKey] = struct{}{}
		}
	} else {
		nodes := append([]*lnrpc.LightningNode{}, g.Nodes...)
		sort.Slice(nodes, func(i, j int) bool {
			di, dj := degree[nodes[i].PubKey], degree[nodes[j].PubKey]
			if di != dj {
				return di > dj
			}
			return nodes[i].PubKey < nodes[j].PubKey
		})
		for _, n := range nodes[:maxNodes] {
			selectedKeys[n.PubKey] = struct{}{}
		}
	}

	nodes := make([]graphNode, 0, len(selectedKeys))
	for _, n := range g.Nodes {
		if _, ok := selectedKeys[n.PubKey]; !ok {
			continue
		}
		nodes = append(nodes, graphNode{PubKey: n.PubKey, Alias: n.Alias})
	}
	sort.Slice(nodes, func(i, j int) bool { return nodes[i].PubKey < nodes[j].PubKey })

	edges := make([]graphEdge, 0, len(g.Edges))
	for _, e := range g.Edges {
		_, ok1 := selectedKeys[e.Node1Pub]
		_, ok2 := selectedKeys[e.Node2Pub]
		if !ok1 || !ok2 {
			continue
		}
		edges = append(edges, graphEdge{
			ChannelID: uint64(e.ChannelId),
			Node1Pub:  e.Node1Pub,
			Node2Pub:  e.Node2Pub,
			Capacity:  e.Capacity,
		})
	}

	return graphResp{
		Nodes: nodes,
		Edges: edges,
		Stats: graphStats{
			Nodes:       len(g.Nodes),
			Channels:    len(g.Edges),
			TotalDCR:    fmt.Sprintf("%.8f", dcrutil.Amount(totalCap).ToCoin()),
			Selected:    len(nodes),
			SelectedChs: len(edges),
		},
	}
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
}

func defaultPath(parts ...string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	all := append([]string{home}, parts...)
	return filepath.Join(all...)
}

func buildClient(rpcServer, tlsCertPath, macaroonPath string, insecureConn bool) (lnrpc.LightningClient, error) {
	opts := make([]grpc.DialOption, 0, 3)
	if insecureConn {
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		creds, err := credentials.NewClientTLSFromFile(tlsCertPath, "")
		if err != nil {
			return nil, fmt.Errorf("load tls cert: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(creds))
	}

	if !insecureConn {
		macBytes, err := os.ReadFile(macaroonPath)
		if err != nil {
			return nil, fmt.Errorf("read macaroon: %w", err)
		}
		opts = append(opts, grpc.WithPerRPCCredentials(macaroonCred{value: hex.EncodeToString(macBytes)}))
	}

	conn, err := grpc.Dial(rpcServer, opts...)
	if err != nil {
		return nil, err
	}
	return lnrpc.NewLightningClient(conn), nil
}

func mockGraph() *lnrpc.ChannelGraph {
	r := rand.New(rand.NewSource(13))
	n := 180
	nodes := make([]*lnrpc.LightningNode, 0, n)
	for i := 0; i < n; i++ {
		key := sha1.Sum([]byte(fmt.Sprintf("node-%d", i)))
		nodes = append(nodes, &lnrpc.LightningNode{PubKey: hex.EncodeToString(key[:]), Alias: fmt.Sprintf("Node-%03d", i)})
	}
	edges := make([]*lnrpc.ChannelEdge, 0, n*3)
	for i := 0; i < n; i++ {
		for _, j := range []int{(i + 3) % n, (i + 19) % n, r.Intn(n)} {
			edges = append(edges, &lnrpc.ChannelEdge{
				ChannelId: uint64(i*1000 + j),
				Node1Pub:  nodes[i].PubKey,
				Node2Pub:  nodes[j].PubKey,
				Capacity:  int64(1e8 + r.Int63n(10e8)),
			})
		}
	}
	return &lnrpc.ChannelGraph{Nodes: nodes, Edges: edges}
}

func main() {
	rpcServer := flag.String("rpcserver", "127.0.0.1:10009", "dcrlnd gRPC host:port")
	listen := flag.String("listen", "127.0.0.1:3007", "address to serve web UI")
	tlsCert := flag.String("tlscert", defaultPath(".dcrlnd", "tls.cert"), "path to dcrlnd TLS cert")
	macaroon := flag.String("macaroon", defaultPath(".dcrlnd", "data", "chain", "decred", "mainnet", "admin.macaroon"), "path to admin.macaroon")
	maxNodes := flag.Int("maxnodes", 180, "default max nodes to render")
	cacheSecs := flag.Int("cache_seconds", 45, "graph cache lifetime in seconds")
	mock := flag.Bool("mock", false, "use generated mock graph instead of live dcrlnd data")
	insecureConn := flag.Bool("insecure", false, "disable TLS+macaroon (debug only)")
	flag.Parse()

	var ln lnrpc.LightningClient
	var err error
	if !*mock {
		ln, err = buildClient(*rpcServer, *tlsCert, *macaroon, *insecureConn)
		if err != nil {
			log.Fatalf("unable to connect to dcrlnd: %v", err)
		}
	}

	a := &app{ln: ln, mock: *mock, maxDefault: *maxNodes, cacheDuration: time.Duration(*cacheSecs) * time.Second}
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/api/graph", a.graphHandler)

	log.Printf("lnviz listening on http://%s", *listen)
	if !*mock {
		log.Printf("connected to %s using tls=%s macaroon=%s", *rpcServer, shortPath(*tlsCert), shortPath(*macaroon))
	}
	log.Fatal(http.ListenAndServe(*listen, nil))
}

func shortPath(p string) string {
	home, _ := os.UserHomeDir()
	if home != "" {
		return strings.Replace(p, home, "~", 1)
	}
	return p
}

const indexHTML = `<!doctype html>
<html>
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Decred LN Visualizer</title>
  <style>
    :root { color-scheme: dark; }
    body { margin: 0; font-family: Inter, system-ui, sans-serif; background:#0b1220; color:#dbe7ff; }
    .wrap { padding:16px; max-width: 1300px; margin: auto; }
    .panel { border:1px solid #2e3c59; border-radius:12px; background:#0e1728; padding:14px; }
    h1 { font-size: 20px; margin:0 0 10px; }
    .controls { display:flex; gap:10px; align-items:center; flex-wrap: wrap; margin-bottom:10px; }
    input, button { border-radius:8px; border:1px solid #435b87; background:#12213d; color:#dbe7ff; padding:6px 10px; }
    button { cursor:pointer; }
    .stat { color:#9ab4e2; font-size:13px; }
    #cv { width:100%; height:660px; border:1px solid #2e3c59; border-radius:10px; background:#0a1325; }
    .hint { font-size: 12px; color:#89a2ce; margin-top: 8px; }
  </style>
</head>
<body>
<div class="wrap">
  <div class="panel">
    <h1>Decred Lightning Network Visualizer</h1>
    <div class="controls">
      <label class="stat">max nodes: <input id="maxNodes" value="180" size="5" /></label>
      <button id="refresh">Refresh</button>
      <span class="stat" id="stats">loading…</span>
    </div>
    <canvas id="cv" width="1250" height="660"></canvas>
    <div class="hint">Tip: drag canvas to pan, mouse wheel to zoom, click a node to inspect aliases/pubkeys.</div>
    <pre id="details" class="hint"></pre>
  </div>
</div>
<script>
const cv = document.getElementById('cv');
const ctx = cv.getContext('2d');
const details = document.getElementById('details');
const statsEl = document.getElementById('stats');
let g = {nodes: [], edges: [], stats: {}};
let pos = new Map();
let scale = 1;
let panX = 0;
let panY = 0;
let dragging = false;
let last = [0,0];

function initPositions(nodes) {
  pos = new Map();
  const cx = cv.width/2, cy = cv.height/2, r = Math.min(cv.width, cv.height) * 0.34;
  nodes.forEach((n, i) => {
    const a = (Math.PI*2*i)/Math.max(1,nodes.length);
    pos.set(n.pub_key, {x: cx + Math.cos(a)*r, y: cy + Math.sin(a)*r, vx:0, vy:0});
  });
}

function stepLayout() {
  const nodes = g.nodes;
  const edges = g.edges;
  for (let k=0; k<2; k++) {
    for (let i=0; i<nodes.length; i++) {
      const pi = pos.get(nodes[i].pub_key);
      for (let j=i+1; j<nodes.length; j++) {
        const pj = pos.get(nodes[j].pub_key);
        let dx = pi.x-pj.x, dy = pi.y-pj.y;
        let d2 = dx*dx+dy*dy+0.01;
        let f = 1500 / d2;
        pi.vx += dx*f; pi.vy += dy*f;
        pj.vx -= dx*f; pj.vy -= dy*f;
      }
    }
    for (const e of edges) {
      const a = pos.get(e.node1_pub), b = pos.get(e.node2_pub);
      if (!a || !b) continue;
      const dx = b.x-a.x, dy = b.y-a.y;
      const dist = Math.hypot(dx,dy)+0.01;
      const desired = 55;
      const f = (dist-desired)*0.003;
      const fx = (dx/dist)*f, fy=(dy/dist)*f;
      a.vx += fx; a.vy += fy;
      b.vx -= fx; b.vy -= fy;
    }
    for (const n of nodes) {
      const p = pos.get(n.pub_key);
      p.vx *= 0.82; p.vy *= 0.82;
      p.x += p.vx; p.y += p.vy;
    }
  }
}

function draw() {
  ctx.clearRect(0,0,cv.width,cv.height);
  ctx.save();
  ctx.translate(panX, panY);
  ctx.scale(scale, scale);

  ctx.strokeStyle = 'rgba(112,139,188,0.35)';
  ctx.lineWidth = 1;
  for (const e of g.edges) {
    const a = pos.get(e.node1_pub), b = pos.get(e.node2_pub);
    if (!a || !b) continue;
    ctx.beginPath(); ctx.moveTo(a.x, a.y); ctx.lineTo(b.x,b.y); ctx.stroke();
  }

  for (const n of g.nodes) {
    const p = pos.get(n.pub_key);
    if (!p) continue;
    ctx.beginPath();
    ctx.fillStyle = '#7fc2ff';
    ctx.arc(p.x, p.y, 2.5, 0, Math.PI*2);
    ctx.fill();
  }

  ctx.restore();
}

function animate() {
  if (g.nodes.length > 0) stepLayout();
  draw();
  requestAnimationFrame(animate);
}

function toWorld(x,y) {
  return {x:(x-panX)/scale, y:(y-panY)/scale};
}

cv.addEventListener('mousedown', (e) => { dragging = true; last=[e.offsetX,e.offsetY]; });
window.addEventListener('mouseup', () => dragging = false);
cv.addEventListener('mousemove', (e) => {
  if (!dragging) return;
  panX += e.offsetX - last[0];
  panY += e.offsetY - last[1];
  last = [e.offsetX,e.offsetY];
});
cv.addEventListener('wheel', (e) => {
  e.preventDefault();
  const factor = e.deltaY > 0 ? 0.92 : 1.08;
  const before = toWorld(e.offsetX, e.offsetY);
  scale *= factor;
  const after = toWorld(e.offsetX, e.offsetY);
  panX += (after.x-before.x)*scale;
  panY += (after.y-before.y)*scale;
}, {passive:false});

cv.addEventListener('click', (e) => {
  const w = toWorld(e.offsetX, e.offsetY);
  let hit = null;
  for (const n of g.nodes) {
    const p = pos.get(n.pub_key);
    if (!p) continue;
    if (Math.hypot(p.x-w.x,p.y-w.y) < 5) { hit=n; break; }
  }
  if (!hit) return;
  details.textContent = 'alias: ' + (hit.alias || '(none)') + '\npubkey: ' + hit.pub_key;
});

async function loadGraph() {
  const maxNodes = encodeURIComponent(document.getElementById('maxNodes').value || '180');
  statsEl.textContent = 'loading…';
  const r = await fetch('/api/graph?max_nodes=' + maxNodes);
  g = await r.json();
  initPositions(g.nodes);
  statsEl.textContent = 'network nodes ' + g.stats.nodes + ' | channels ' + g.stats.channels + ' | selected ' + g.stats.selected_nodes + '/' + g.stats.selected_channels + ' | total cap ' + g.stats.total_dcr + ' DCR';
  details.textContent = '';
}

document.getElementById('refresh').onclick = loadGraph;
loadGraph().catch(err => statsEl.textContent = 'error: ' + err);
animate();
</script>
</body></html>`
