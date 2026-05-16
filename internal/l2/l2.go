package l2

import (
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/vishvananda/netlink"
)

var zeroMAC, _ = net.ParseMAC("00:00:00:00:00:00")

// Up ensures the bridge and VXLAN interfaces exist, attaches the VXLAN to the
// bridge, attaches any configured local ports, and brings everything up.
// Idempotent.
func Up(s *state.State) error {
	overlay := net.ParseIP(s.Node.OverlayIP)
	if overlay == nil || overlay.To4() == nil {
		return fmt.Errorf("invalid overlay IPv4 %q", s.Node.OverlayIP)
	}

	bridge, err := ensureBridge(s.L2.BridgeIface, int(s.L2.MTU))
	if err != nil {
		return err
	}

	vxlan, err := ensureVxlan(s.L2.VxlanIface, int(s.L2.VNI), int(s.L2.Port), int(s.L2.MTU), overlay.To4())
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

func ensureVxlan(name string, vni, port, mtu int, local net.IP) (netlink.Link, error) {
	if link, err := netlink.LinkByName(name); err == nil {
		return link, nil
	} else if !isNotFound(err) {
		return nil, fmt.Errorf("lookup %s: %w", name, err)
	}
	v := &netlink.Vxlan{
		LinkAttrs: netlink.LinkAttrs{Name: name, MTU: mtu},
		VxlanId:   vni,
		SrcAddr:   local,
		Port:      port,
		Learning:  true,
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
