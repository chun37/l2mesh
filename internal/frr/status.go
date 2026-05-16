package frr

import (
	"encoding/json"
	"fmt"
	"os/exec"
)

// Status is a snapshot of FRR + EVPN state, populated by GetStatus.
type Status struct {
	Available bool
	RouterID  string
	LocalAS   uint32
	Peers     []PeerStatus
	VNI       *VNIStatus
}

type PeerStatus struct {
	Address  string
	Hostname string
	State    string
	PfxRcvd  int
	PfxSent  int
	UpFor    string
}

type VNIStatus struct {
	VNI               uint32
	Type              string
	VxlanIfname       string
	BridgeIfname      string
	NumMACs           int
	NumARPs           int
	NumRemoteVTEPs    int
	AdvertiseSVIMacIP string
}

// PeerByOverlayIP returns the BGP peer status for the given overlay IP, or nil.
func (s *Status) PeerByOverlayIP(ip string) *PeerStatus {
	for i := range s.Peers {
		if s.Peers[i].Address == ip {
			return &s.Peers[i]
		}
	}
	return nil
}

// GetStatus queries vtysh for BGP/EVPN state. Returns Available=false (and no
// error) when FRR is not installed or not responding, so callers can present a
// partial status without failing.
func GetStatus(vni uint32) Status {
	s := Status{}
	if !Installed() {
		return s
	}
	sum, ok := bgpSummary()
	if !ok {
		return s
	}
	s.Available = true
	s.RouterID = sum.RouterID
	s.LocalAS = sum.AS
	for addr, p := range sum.Peers {
		s.Peers = append(s.Peers, PeerStatus{
			Address:  addr,
			Hostname: p.Hostname,
			State:    p.State,
			PfxRcvd:  p.PfxRcvd,
			PfxSent:  p.PfxSent,
			UpFor:    p.PeerUptime,
		})
	}
	if v, ok := vniDetail(vni); ok {
		s.VNI = &v
	}
	return s
}

type bgpSummaryJSON struct {
	RouterID string                        `json:"routerId"`
	AS       uint32                        `json:"as"`
	Peers    map[string]bgpSummaryPeerJSON `json:"peers"`
}

type bgpSummaryPeerJSON struct {
	Hostname   string `json:"hostname"`
	State      string `json:"state"`
	PfxRcvd    int    `json:"pfxRcd"`
	PfxSent    int    `json:"pfxSnt"`
	PeerUptime string `json:"peerUptime"`
}

func bgpSummary() (bgpSummaryJSON, bool) {
	out, err := runVtysh("show bgp l2vpn evpn summary json")
	if err != nil {
		return bgpSummaryJSON{}, false
	}
	var s bgpSummaryJSON
	if err := json.Unmarshal(out, &s); err != nil {
		return bgpSummaryJSON{}, false
	}
	if s.RouterID == "" {
		return bgpSummaryJSON{}, false
	}
	return s, true
}

type vniJSON struct {
	VNI               uint32 `json:"vni"`
	Type              string `json:"type"`
	VxlanInterface    string `json:"vxlanInterface"`
	SVIInterface      string `json:"sviInterface"`
	NumMACs           int    `json:"numMacs"`
	NumARPs           int    `json:"numArpNd"`
	NumRemoteVTEPs    int    `json:"numRemoteVteps"`
	AdvertiseSVIMacIP string `json:"advertiseSviMacip"`
}

func vniDetail(vni uint32) (VNIStatus, bool) {
	out, err := runVtysh(fmt.Sprintf("show evpn vni %d json", vni))
	if err != nil {
		return VNIStatus{}, false
	}
	var v vniJSON
	if err := json.Unmarshal(out, &v); err != nil || v.VNI == 0 {
		return VNIStatus{}, false
	}
	return VNIStatus{
		VNI:               v.VNI,
		Type:              v.Type,
		VxlanIfname:       v.VxlanInterface,
		BridgeIfname:      v.SVIInterface,
		NumMACs:           v.NumMACs,
		NumARPs:           v.NumARPs,
		NumRemoteVTEPs:    v.NumRemoteVTEPs,
		AdvertiseSVIMacIP: v.AdvertiseSVIMacIP,
	}, true
}

func runVtysh(cmd string) ([]byte, error) {
	c := exec.Command("vtysh", "-c", cmd)
	return c.Output()
}
