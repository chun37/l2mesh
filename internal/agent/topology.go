package agent

import (
	"sort"
	"sync"
	"time"
)

// NodeInfo is what each agent publishes about itself over the gossip channel.
type NodeInfo struct {
	Name        string    `json:"name"`
	OverlayIP   string    `json:"overlay_ip"`
	PeerIPs     []string  `json:"peer_ips"`      // all configured peers
	LivePeerIPs []string  `json:"live_peer_ips"` // peers with recent WG handshake
	UpdatedAt   time.Time `json:"updated_at"`
}

// Edge is an undirected edge in the overlay graph. Endpoints are sorted
// (A <= B lexicographically) so the same edge collapses to one entry no
// matter from which direction it's reported.
type Edge struct {
	A, B string
}

func newEdge(x, y string) Edge {
	if x <= y {
		return Edge{A: x, B: y}
	}
	return Edge{A: y, B: x}
}

// Topology holds the most recent NodeInfo seen for every known overlay node.
// It's the agent's local view of the global mesh — eventually consistent via
// gossip.
type Topology struct {
	mu    sync.RWMutex
	nodes map[string]NodeInfo
}

func NewTopology() *Topology {
	return &Topology{nodes: make(map[string]NodeInfo)}
}

func (t *Topology) Set(info NodeInfo) {
	t.mu.Lock()
	defer t.mu.Unlock()
	info.UpdatedAt = time.Now()
	t.nodes[info.OverlayIP] = info
}

func (t *Topology) Snapshot() map[string]NodeInfo {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make(map[string]NodeInfo, len(t.nodes))
	for k, v := range t.nodes {
		out[k] = v
	}
	return out
}

// PurgeStale drops entries older than maxAge. We never purge the local node
// (whose info gets refreshed every tick), so it's safe to use even when the
// agent is the only running node in the mesh.
func (t *Topology) PurgeStale(maxAge time.Duration, exclude string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	cutoff := time.Now().Add(-maxAge)
	removed := 0
	for k, v := range t.nodes {
		if k == exclude {
			continue
		}
		if v.UpdatedAt.Before(cutoff) {
			delete(t.nodes, k)
			removed++
		}
	}
	return removed
}

// NodeIPs returns the sorted list of overlay IPs known to the topology.
func (t *Topology) NodeIPs() []string {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]string, 0, len(t.nodes))
	for k := range t.nodes {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Edges returns unique undirected edges where at least one endpoint reports
// the other as a live peer. Being forgiving about asymmetry avoids stalls
// during convergence when one side has just (re)established a session.
func (t *Topology) Edges() []Edge {
	snap := t.Snapshot()
	seen := map[Edge]bool{}
	out := make([]Edge, 0)
	for ip, info := range snap {
		for _, peer := range info.LivePeerIPs {
			e := newEdge(ip, peer)
			if !seen[e] {
				seen[e] = true
				out = append(out, e)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].A != out[j].A {
			return out[i].A < out[j].A
		}
		return out[i].B < out[j].B
	})
	return out
}
