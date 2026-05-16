package l2

import (
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/vishvananda/netlink"
	"golang.org/x/sys/unix"
)

var zeroMAC, _ = net.ParseMAC("00:00:00:00:00:00")

// Up ensures the bridge and VXLAN interfaces exist, attaches the VXLAN to the
// bridge, attaches any configured local ports, applies configured bridge IP
// addresses, and brings everything up. Idempotent.
func Up(s *state.State) error {
	overlay := net.ParseIP(s.Node.OverlayIP)
	if overlay == nil || overlay.To4() == nil {
		return fmt.Errorf("invalid overlay IPv4 %q", s.Node.OverlayIP)
	}

	bridge, err := ensureBridge(s.L2.BridgeIface, int(s.L2.MTU))
	if err != nil {
		return err
	}

	if err := syncBridgeAddrs(bridge, s.L2.BridgeAddrs); err != nil {
		return err
	}

	// Plan B: every node runs FRR/EVPN, so VXLAN is always nolearning. The FDB
	// is populated by BGP Type-2 routes; kernel auto-learning would race EVPN
	// and reintroduce loops in 3+ node meshes.
	vxlan, err := ensureVxlan(s.L2.VxlanIface, int(s.L2.VNI), int(s.L2.Port), int(s.L2.MTU), overlay.To4(), false)
	if err != nil {
		return err
	}

	if err := netlink.LinkSetMaster(vxlan, bridge); err != nil {
		return fmt.Errorf("attach %s to %s: %w", s.L2.VxlanIface, s.L2.BridgeIface, err)
	}

	for _, port := range s.L2.LocalPorts {
		link, err := netlink.LinkByName(port)
		if err != nil {
			return fmt.Errorf("local port %s: %w", port, err)
		}
		if err := netlink.LinkSetMaster(link, bridge); err != nil {
			return fmt.Errorf("attach local port %s: %w", port, err)
		}
		if err := netlink.LinkSetUp(link); err != nil {
			return fmt.Errorf("up local port %s: %w", port, err)
		}
	}

	if err := netlink.LinkSetUp(vxlan); err != nil {
		return fmt.Errorf("up %s: %w", s.L2.VxlanIface, err)
	}
	if err := netlink.LinkSetUp(bridge); err != nil {
		return fmt.Errorf("up %s: %w", s.L2.BridgeIface, err)
	}
	return nil
}

// Down deletes the VXLAN and bridge if they exist. Idempotent.
func Down(s *state.State) error {
	for _, name := range []string{s.L2.VxlanIface, s.L2.BridgeIface} {
		link, err := netlink.LinkByName(name)
		if err != nil {
			var lnf netlink.LinkNotFoundError
			if errors.As(err, &lnf) {
				continue
			}
			return fmt.Errorf("lookup %s: %w", name, err)
		}
		if err := netlink.LinkDel(link); err != nil {
			return fmt.Errorf("del %s: %w", name, err)
		}
	}
	return nil
}

// SyncFDB reconciles the VXLAN broadcast/unknown/multicast (BUM) entries with
// the desired peer VTEP list, adding/removing entries as needed. Pass the
// overlay IPs of all peers whose traffic should be flooded to.
func SyncFDB(s *state.State, peerVTEPs []string) error {
	vxlan, err := netlink.LinkByName(s.L2.VxlanIface)
	if err != nil {
		return fmt.Errorf("lookup %s: %w", s.L2.VxlanIface, err)
	}

	current, err := netlink.NeighList(vxlan.Attrs().Index, syscall.AF_BRIDGE)
	if err != nil {
		return fmt.Errorf("list fdb: %w", err)
	}

	existing := map[string]bool{}
	for _, n := range current {
		if n.HardwareAddr.String() != zeroMAC.String() {
			continue
		}
		if n.IP != nil {
			existing[n.IP.String()] = true
		}
	}

	desired := map[string]bool{}
	for _, ip := range peerVTEPs {
		desired[ip] = true
	}

	for ip := range desired {
		if existing[ip] {
			continue
		}
		if err := fdbAppend(vxlan, ip); err != nil {
			return err
		}
	}
	for ip := range existing {
		if desired[ip] {
			continue
		}
		if err := fdbDel(vxlan, ip); err != nil {
			return err
		}
	}
	return nil
}

func ensureBridge(name string, mtu int) (netlink.Link, error) {
	if link, err := netlink.LinkByName(name); err == nil {
		return link, nil
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("lookup %s: %w", name, err)
	}
	br := &netlink.Bridge{LinkAttrs: netlink.LinkAttrs{Name: name, MTU: mtu}}
	if err := netlink.LinkAdd(br); err != nil {
		return nil, fmt.Errorf("add bridge %s: %w", name, err)
	}
	return netlink.LinkByName(name)
}

func ensureVxlan(name string, vni, port, mtu int, local net.IP, learning bool) (netlink.Link, error) {
	if link, err := netlink.LinkByName(name); err == nil {
		// Recreate if the learning flag drifted (e.g. role flipped between
		// leaf and root); kernel doesn't expose the attribute as mutable.
		if vx, ok := link.(*netlink.Vxlan); ok && vx.Learning == learning {
			return link, nil
		}
		if err := netlink.LinkDel(link); err != nil {
			return nil, fmt.Errorf("recreate %s (learning flag changed): %w", name, err)
		}
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("lookup %s: %w", name, err)
	}
	v := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{Name: name, MTU: mtu},
		VxlanId:   vni,
		SrcAddr:   local,
		Port:      port,
		Learning:  learning,
	}
	if err := netlink.LinkAdd(v); err != nil {
		return nil, fmt.Errorf("add vxlan %s: %w", name, err)
	}
	return netlink.LinkByName(name)
}

func fdbAppend(vxlan netlink.Link, ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid peer VTEP IP %q", ip)
	}
	neigh := &netlink.Neigh{
		LinkIndex:    vxlan.Attrs().Index,
		Family:       syscall.AF_BRIDGE,
		State:        netlink.NUD_PERMANENT,
		Flags:        netlink.NTF_SELF,
		HardwareAddr: zeroMAC,
		IP:           parsed,
	}
	if err := netlink.NeighAppend(neigh); err != nil {
		return fmt.Errorf("fdb append %s: %w", ip, err)
	}
	return nil
}

func fdbDel(vxlan netlink.Link, ip string) error {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		return fmt.Errorf("invalid peer VTEP IP %q", ip)
	}
	neigh := &netlink.Neigh{
		LinkIndex:    vxlan.Attrs().Index,
		Family:       syscall.AF_BRIDGE,
		Flags:        netlink.NTF_SELF,
		HardwareAddr: zeroMAC,
		IP:           parsed,
	}
	if err := netlink.NeighDel(neigh); err != nil {
		return fmt.Errorf("fdb del %s: %w", ip, err)
	}
	return nil
}

func isNotFound(err error) bool {
	var lnf netlink.LinkNotFoundError
	return errors.As(err, &lnf)
}

// syncBridgeAddrs makes the bridge's addresses match desired exactly. Only
// global-scope addresses are reconciled — kernel-assigned link-local (fe80::)
// is left alone.
func syncBridgeAddrs(bridge netlink.Link, desired []string) error {
	wanted := make([]*netlink.Addr, 0, len(desired))
	for _, cidr := range desired {
		addr, err := netlink.ParseAddr(cidr)
		if err != nil {
			return fmt.Errorf("parse bridge addr %q: %w", cidr, err)
		}
		wanted = append(wanted, addr)
	}

	current, err := netlink.AddrList(bridge, netlink.FAMILY_ALL)
	if err != nil {
		return fmt.Errorf("list bridge addrs: %w", err)
	}

	for _, want := range wanted {
		if hasAddr(current, want) {
			continue
		}
		if err := netlink.AddrAdd(bridge, want); err != nil {
			return fmt.Errorf("add bridge addr %s: %w", want.IPNet.String(), err)
		}
	}
	for i := range current {
		have := &current[i]
		if have.Scope != unix.RT_SCOPE_UNIVERSE {
			continue
		}
		if hasAddr(wantedAsAddrs(wanted), have) {
			continue
		}
		if err := netlink.AddrDel(bridge, have); err != nil {
			return fmt.Errorf("del bridge addr %s: %w", have.IPNet.String(), err)
		}
	}
	return nil
}

func hasAddr(list []netlink.Addr, target *netlink.Addr) bool {
	for i := range list {
		if list[i].Equal(*target) {
			return true
		}
	}
	return false
}

func wantedAsAddrs(p []*netlink.Addr) []netlink.Addr {
	out := make([]netlink.Addr, len(p))
	for i, a := range p {
		out[i] = *a
	}
	return out
}
