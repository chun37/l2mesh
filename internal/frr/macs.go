package frr

import (
	"encoding/json"
	"fmt"
	"sort"
)

// MACEntry merges one MAC from `show evpn mac vni N json` with the IPs that
// the EVPN ARP cache associates with it (so callers can show MAC + IPs +
// origin in one row).
type MACEntry struct {
	MAC        string
	Type       string // "local" | "remote"
	Interface  string // local only
	VLAN       int    // local only
	RemoteVTEP string // remote only
	IPs        []string
}

type macJSON struct {
	Type       string `json:"type"`
	Intf       string `json:"intf"`
	VLAN       int    `json:"vlan"`
	RemoteVtep string `json:"remoteVtep"`
}

type macTable struct {
	NumMACs int                `json:"numMacs"`
	MACs    map[string]macJSON `json:"macs"`
}

type arpJSON struct {
	Type       string `json:"type"`
	State      string `json:"state"`
	MAC        string `json:"mac"`
	RemoteVtep string `json:"remoteVtep"`
}

// GetMACs queries vtysh for EVPN MAC + ARP info on the given VNI and returns
// merged entries sorted local-first then by MAC.
func GetMACs(vni uint32) ([]MACEntry, error) {
	if !Installed() {
		return nil, fmt.Errorf("vtysh not in PATH; is FRR installed?")
	}

	macData, err := runVtysh(fmt.Sprintf("show evpn mac vni %d json", vni))
	if err != nil {
		return nil, fmt.Errorf("vtysh mac: %w", err)
	}
	var mt macTable
	if err := json.Unmarshal(macData, &mt); err != nil {
		return nil, fmt.Errorf("parse mac json: %w", err)
	}

	macToIPs := arpByMAC(vni)

	out := make([]MACEntry, 0, len(mt.MACs))
	for mac, info := range mt.MACs {
		ips := macToIPs[mac]
		sort.Strings(ips)
		out = append(out, MACEntry{
			MAC:        mac,
			Type:       info.Type,
			Interface:  info.Intf,
			VLAN:       info.VLAN,
			RemoteVTEP: info.RemoteVtep,
			IPs:        ips,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Type != out[j].Type {
			return out[i].Type == "local"
		}
		return out[i].MAC < out[j].MAC
	})
	return out, nil
}

// arpByMAC fetches the EVPN ARP/ND cache and inverts it to MAC -> []IP.
// Failures are silent; an empty map is returned so MAC listing keeps working
// even if the ARP query is unavailable.
func arpByMAC(vni uint32) map[string][]string {
	data, err := runVtysh(fmt.Sprintf("show evpn arp-cache vni %d json", vni))
	if err != nil {
		return nil
	}
	// Top-level keys are a mix of IP addresses (object values) and metadata
	// like "numArpNd" (number). Decode each value lazily and skip ones that
	// don't match our entry shape.
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	out := map[string][]string{}
	for ip, val := range raw {
		var entry arpJSON
		if err := json.Unmarshal(val, &entry); err != nil {
			continue
		}
		if entry.MAC == "" {
			continue
		}
		out[entry.MAC] = append(out[entry.MAC], ip)
	}
	return out
}
