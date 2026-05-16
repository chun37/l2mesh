package state

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"golang.org/x/sys/unix"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

const DefaultPath = "/var/lib/l2mesh/state.json"

type Role string

const (
	RoleRoot Role = "root"
	RoleLeaf Role = "leaf"
)

type Node struct {
	Name       string `json:"name"`
	Role       Role   `json:"role"`
	OverlayIP  string `json:"overlay_ip"`
	Endpoint   string `json:"endpoint,omitempty"`
	ASN        uint32 `json:"asn,omitempty"`
	ListenPort int    `json:"listen_port"`
	Interface  string `json:"interface"`
}

type Peer struct {
	Name      string `json:"name"`
	PublicKey string `json:"pubkey"`
	OverlayIP string `json:"overlay_ip"`
	Endpoint  string `json:"endpoint,omitempty"`
}

// AnnotatedPeer is a Peer paired with its Role, for display.
type AnnotatedPeer struct {
	Peer
	Kind Role
}

type State struct {
	Node  Node   `json:"node"`
	Roots []Peer `json:"roots"`
	Leafs []Peer `json:"leafs"`
}

// defaultState returns a placeholder used when no state.json exists yet.
// Operators are expected to overwrite Node fields via `l2mesh init` or by
// editing the file before running other commands.
func defaultState() State {
	return State{
		Node: Node{
			Name:       "unconfigured",
			Role:       RoleRoot,
			OverlayIP:  "100.64.0.1",
			Endpoint:   "",
			ASN:        65000,
			ListenPort: 51820,
			Interface:  "wg-l2mesh",
		},
		Roots: []Peer{},
		Leafs: []Peer{},
	}
}

func Load(path string) (*State, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		s := defaultState()
		return &s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read state: %w", err)
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("parse state: %w", err)
	}
	return &s, nil
}

func (s *State) Save(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// WithLock serializes mutating operations across CLI invocations via flock
// on a sibling lock file, so two concurrent commands can't lose writes.
func WithLock(path string, fn func(*State) error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("mkdir state dir: %w", err)
	}
	lock, err := os.OpenFile(path+".lock", os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("open lock: %w", err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		return fmt.Errorf("flock: %w", err)
	}
	s, err := Load(path)
	if err != nil {
		return err
	}
	if err := fn(s); err != nil {
		return err
	}
	return s.Save(path)
}

func (s *State) FindRoot(name string) (int, *Peer) {
	for i := range s.Roots {
		if s.Roots[i].Name == name {
			return i, &s.Roots[i]
		}
	}
	return -1, nil
}

func (s *State) FindLeaf(name string) (int, *Peer) {
	for i := range s.Leafs {
		if s.Leafs[i].Name == name {
			return i, &s.Leafs[i]
		}
	}
	return -1, nil
}

func (s *State) FindByPubkey(pubkey string) (Role, *Peer) {
	for i := range s.Roots {
		if s.Roots[i].PublicKey == pubkey {
			return RoleRoot, &s.Roots[i]
		}
	}
	for i := range s.Leafs {
		if s.Leafs[i].PublicKey == pubkey {
			return RoleLeaf, &s.Leafs[i]
		}
	}
	return "", nil
}

func (s *State) FindByOverlayIP(ip string) (Role, *Peer) {
	for i := range s.Roots {
		if s.Roots[i].OverlayIP == ip {
			return RoleRoot, &s.Roots[i]
		}
	}
	for i := range s.Leafs {
		if s.Leafs[i].OverlayIP == ip {
			return RoleLeaf, &s.Leafs[i]
		}
	}
	return "", nil
}

func (s *State) AddPeer(role Role, p Peer) error {
	if _, err := wgtypes.ParseKey(p.PublicKey); err != nil {
		return fmt.Errorf("invalid pubkey: %w", err)
	}
	if kind, existing := s.FindByPubkey(p.PublicKey); existing != nil {
		return fmt.Errorf("pubkey already used by %s peer %q", kind, existing.Name)
	}
	if kind, existing := s.FindByOverlayIP(p.OverlayIP); existing != nil {
		return fmt.Errorf("overlay IP %s already used by %s peer %q", p.OverlayIP, kind, existing.Name)
	}
	switch role {
	case RoleRoot:
		if _, existing := s.FindRoot(p.Name); existing != nil {
			return fmt.Errorf("root %q already exists", p.Name)
		}
		s.Roots = append(s.Roots, p)
	case RoleLeaf:
		if _, existing := s.FindLeaf(p.Name); existing != nil {
			return fmt.Errorf("leaf %q already exists", p.Name)
		}
		s.Leafs = append(s.Leafs, p)
	default:
		return fmt.Errorf("unknown role %q", role)
	}
	return nil
}

func (s *State) RemovePeer(role Role, name string) (string, error) {
	switch role {
	case RoleRoot:
		idx, p := s.FindRoot(name)
		if p == nil {
			return "", fmt.Errorf("root %q not found", name)
		}
		pubkey := p.PublicKey
		s.Roots = append(s.Roots[:idx], s.Roots[idx+1:]...)
		return pubkey, nil
	case RoleLeaf:
		idx, p := s.FindLeaf(name)
		if p == nil {
			return "", fmt.Errorf("leaf %q not found", name)
		}
		pubkey := p.PublicKey
		s.Leafs = append(s.Leafs[:idx], s.Leafs[idx+1:]...)
		return pubkey, nil
	default:
		return "", fmt.Errorf("unknown role %q", role)
	}
}

func (s *State) FlatPeers() []Peer {
	out := make([]Peer, 0, len(s.Roots)+len(s.Leafs))
	out = append(out, s.Roots...)
	out = append(out, s.Leafs...)
	return out
}

func (s *State) AllPeers() []AnnotatedPeer {
	out := make([]AnnotatedPeer, 0, len(s.Roots)+len(s.Leafs))
	for _, p := range s.Roots {
		out = append(out, AnnotatedPeer{Peer: p, Kind: RoleRoot})
	}
	for _, p := range s.Leafs {
		out = append(out, AnnotatedPeer{Peer: p, Kind: RoleLeaf})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return out[i].Name < out[j].Name
	})
	return out
}
