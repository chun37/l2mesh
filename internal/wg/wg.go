package wg

import (
	"fmt"
	"net"
	"time"

	"github.com/chun37/l2mesh/internal/state"
	"golang.zx2c4.com/wireguard/wgctrl"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

type Client struct {
	c     *wgctrl.Client
	iface string
}

func New(iface string) (*Client, error) {
	c, err := wgctrl.New()
	if err != nil {
		return nil, fmt.Errorf("wgctrl new: %w", err)
	}
	return &Client{c: c, iface: iface}, nil
}

func (w *Client) Close() error {
	return w.c.Close()
}

func (w *Client) Device() (*wgtypes.Device, error) {
	return w.c.Device(w.iface)
}

func peerConfig(p state.Peer) (wgtypes.PeerConfig, error) {
	key, err := wgtypes.ParseKey(p.PublicKey)
	if err != nil {
		return wgtypes.PeerConfig{}, fmt.Errorf("parse pubkey: %w", err)
	}
	allowed, err := overlayAllowedIPs(p.OverlayIP)
	if err != nil {
		return wgtypes.PeerConfig{}, err
	}
	cfg := wgtypes.PeerConfig{
		PublicKey:         key,
		ReplaceAllowedIPs: true,
		AllowedIPs:        allowed,
	}
	if p.Endpoint != "" {
		ep, err := net.ResolveUDPAddr("udp", p.Endpoint)
		if err != nil {
			return wgtypes.PeerConfig{}, fmt.Errorf("resolve endpoint %q: %w", p.Endpoint, err)
		}
		cfg.Endpoint = ep
		ka := 25 * time.Second
		cfg.PersistentKeepaliveInterval = &ka
	}
	return cfg, nil
}

func overlayAllowedIPs(ip string) ([]net.IPNet, error) {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return nil, fmt.Errorf("invalid overlay IP %q", ip)
	}
	v4 := parsed.To4()
	if v4 == nil {
		return nil, fmt.Errorf("overlay IP %q is not IPv4", ip)
	}
	return []net.IPNet{{IP: v4, Mask: net.CIDRMask(32, 32)}}, nil
}

func (w *Client) AddOrUpdatePeer(p state.Peer) error {
	cfg, err := peerConfig(p)
	if err != nil {
		return err
	}
	return w.c.ConfigureDevice(w.iface, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{cfg},
	})
}

func (w *Client) RemovePeer(pubkey string) error {
	key, err := wgtypes.ParseKey(pubkey)
	if err != nil {
		return fmt.Errorf("parse pubkey: %w", err)
	}
	return w.c.ConfigureDevice(w.iface, wgtypes.Config{
		Peers: []wgtypes.PeerConfig{{
			PublicKey: key,
			Remove:    true,
		}},
	})
}

// Sync replaces all kernel peers with the given set. Used by `l2mesh sync`
// (and the boot-time systemd unit) to make the kernel authoritative against
// state.json after a reboot or external drift.
func (w *Client) Sync(peers []state.Peer) error {
	cfgs := make([]wgtypes.PeerConfig, 0, len(peers))
	for _, p := range peers {
		cfg, err := peerConfig(p)
		if err != nil {
			return fmt.Errorf("peer %s: %w", p.Name, err)
		}
		cfgs = append(cfgs, cfg)
	}
	return w.c.ConfigureDevice(w.iface, wgtypes.Config{
		ReplacePeers: true,
		Peers:        cfgs,
	})
}
