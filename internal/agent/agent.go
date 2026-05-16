package agent

import (
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/chun37/l2mesh/internal/state"
	"github.com/chun37/l2mesh/internal/wg"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

// Agent runs a gossip server, polls direct peers for their NodeInfo, computes
// an MST over the union, and rewrites the vxlan BUM FDB to only the local
// MST edges. It owns BUM forwarding while it's running.
type Agent struct {
	statePath  string
	topology   *Topology
	interval   time.Duration
	stale      time.Duration
	livenessTO time.Duration
	logger     *log.Logger

	mu          sync.Mutex
	lastTreeIPs []string // last applied BUM set, for change detection
}

type Option func(*Agent)

func WithInterval(d time.Duration) Option   { return func(a *Agent) { a.interval = d } }
func WithStaleAfter(d time.Duration) Option { return func(a *Agent) { a.stale = d } }
func WithLogOutput(w io.Writer) Option {
	return func(a *Agent) { a.logger = log.New(w, "l2mesh-agent ", log.LstdFlags) }
}

func New(statePath string, opts ...Option) *Agent {
	a := &Agent{
		statePath:  statePath,
		topology:   NewTopology(),
		interval:   5 * time.Second,
		stale:      90 * time.Second,
		livenessTO: 3 * time.Minute,
		logger:     log.New(io.Discard, "", 0),
	}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

func (a *Agent) Run(ctx context.Context) error {
	s, err := state.Load(a.statePath)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	srv := NewServer(a.computeSelfInfo, a.topology.Snapshot)
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- srv.ListenAndServe(ctx, s.Node.OverlayIP)
	}()

	a.tick(ctx)

	t := time.NewTicker(a.interval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil
		case err := <-serverErr:
			if err != nil {
				return err
			}
		case <-t.C:
			a.tick(ctx)
		}
	}
}

// computeSelfInfo builds the NodeInfo we serve at /info. PeerIPs is the
// configured set; LivePeerIPs is filtered by recent WG handshake so MST sees
// the real connectivity, not the wishlist.
func (a *Agent) computeSelfInfo() NodeInfo {
	s, err := state.Load(a.statePath)
	if err != nil {
		a.logger.Printf("load state for self info: %v", err)
		return NodeInfo{}
	}
	peers := make([]string, 0, len(s.Roots)+len(s.Leafs))
	for _, p := range s.AllPeers() {
		peers = append(peers, p.OverlayIP)
	}
	sort.Strings(peers)

	live := a.livePeers(s)
	return NodeInfo{
		Name:        s.Node.Name,
		OverlayIP:   s.Node.OverlayIP,
		PeerIPs:     peers,
		LivePeerIPs: live,
		UpdatedAt:   time.Now(),
	}
}

func (a *Agent) livePeers(s *state.State) []string {
	c, err := wg.New(s.Node.Interface)
	if err != nil {
		return nil
	}
	defer c.Close()
	dev, err := c.Device()
	if err != nil {
		return nil
	}
	alive := map[wgtypes.Key]bool{}
	for _, kp := range dev.Peers {
		if kp.LastHandshakeTime.IsZero() {
			continue
		}
		if time.Since(kp.LastHandshakeTime) < a.livenessTO {
			alive[kp.PublicKey] = true
		}
	}
	out := make([]string, 0)
	for _, p := range s.AllPeers() {
		k, err := wgtypes.ParseKey(p.PublicKey)
		if err != nil {
			continue
		}
		if alive[k] {
			out = append(out, p.OverlayIP)
		}
	}
	sort.Strings(out)
	return out
}

func (a *Agent) tick(ctx context.Context) {
	s, err := state.Load(a.statePath)
	if err != nil {
		a.logger.Printf("load state: %v", err)
		return
	}

	a.topology.Set(a.computeSelfInfo())

	for _, p := range s.AllPeers() {
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		info, err := FetchInfo(c, p.OverlayIP)
		cancel()
		if err != nil {
			a.logger.Printf("fetch %s (%s): %v", p.Name, p.OverlayIP, err)
			continue
		}
		a.topology.Set(info)
	}

	a.topology.PurgeStale(a.stale, s.Node.OverlayIP)

	ips := a.topology.NodeIPs()
	edges := a.topology.Edges()
	mst := ComputeMST(ips, edges)
	treeIPs := LocalNeighbors(s.Node.OverlayIP, mst)

	a.mu.Lock()
	changed := !sameStrings(a.lastTreeIPs, treeIPs)
	a.lastTreeIPs = append(a.lastTreeIPs[:0], treeIPs...)
	a.mu.Unlock()

	if !changed {
		return
	}

	a.logger.Printf("MST: %d nodes, %d edges, local tree neighbors: [%s]",
		len(ips), len(mst), strings.Join(treeIPs, ", "))

	// Push the new MST_VTEPS prefix-list into FRR via frr.Apply. The FRR
	// route-map MST_T3 then filters Type-3 routes by next-hop, so the kernel
	// BUM FDB (which zebra derives from accepted Type-3 routes) ends up
	// constrained to the MST.
	if err := frr.Apply(s, treeIPs); err != nil {
		a.logger.Printf("frr apply (MST_VTEPS=%v): %v", treeIPs, err)
	}
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
