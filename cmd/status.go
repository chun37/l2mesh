package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/chun37/l2mesh/internal/state"
	"github.com/chun37/l2mesh/internal/wg"
	"github.com/spf13/cobra"
	"golang.zx2c4.com/wireguard/wgctrl/wgtypes"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show node role, WireGuard interface, and peer status",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}

		fmt.Printf("Node:      %s (role=%s)\n", s.Node.Name, s.Node.Role)
		fmt.Printf("Overlay:   %s\n", s.Node.OverlayIP)
		fmt.Printf("Endpoint:  %s\n", s.Node.Endpoint)
		fmt.Printf("Interface: %s (listen %d)\n", s.Node.Interface, s.Node.ListenPort)
		fmt.Println()

		client, err := wg.New(s.Node.Interface)
		if err != nil {
			fmt.Printf("(WireGuard interface unavailable: %v)\n", err)
			return nil
		}
		defer client.Close()

		dev, err := client.Device()
		if err != nil {
			return err
		}

		live := map[wgtypes.Key]wgtypes.Peer{}
		for _, p := range dev.Peers {
			live[p.PublicKey] = p
		}

		fmt.Printf("Configured peers: %d (state) / %d (kernel)\n\n",
			len(s.Roots)+len(s.Leafs), len(dev.Peers))

		fr := frr.GetStatus(s.L2.VNI)

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tNAME\tOVERLAY\tENDPOINT\tHANDSHAKE\tWG\tBGP\tTREE")
		treeCount := 0
		for _, p := range s.AllPeers() {
			key, err := wgtypes.ParseKey(p.PublicKey)
			handshake := "-"
			wgState := "unknown"
			if err == nil {
				if kp, ok := live[key]; ok {
					if kp.LastHandshakeTime.IsZero() {
						handshake = "never"
						wgState = "pending"
					} else {
						age := time.Since(kp.LastHandshakeTime).Round(time.Second)
						handshake = age.String() + " ago"
						if age < 3*time.Minute {
							wgState = "alive"
						} else {
							wgState = "stale"
						}
					}
				} else {
					wgState = "missing-in-kernel"
				}
			} else {
				wgState = "bad-pubkey"
			}
			ep := p.Endpoint
			if ep == "" {
				ep = "(dynamic)"
			}
			bgpState := bgpColumn(fr, p)
			treeStr := "yes"
			if !p.IsTreeNeighbor() {
				treeStr = "no"
			} else {
				treeCount++
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
				p.Kind, p.Name, p.OverlayIP, ep, handshake, wgState, bgpState, treeStr)
		}
		if err := tw.Flush(); err != nil {
			return err
		}

		if len(s.AllPeers()) > 0 && treeCount == 0 {
			fmt.Fprintln(cmd.ErrOrStderr(),
				"\nwarning: every peer has tree_neighbor=false — BUM (broadcast/ARP/etc.) won't reach anyone")
		}

		printL2Section(cmd, s)
		printFRRSection(cmd, &fr)
		return nil
	},
}

func bgpColumn(fr frr.Status, p state.AnnotatedPeer) string {
	if !fr.Available {
		return "-"
	}
	peer := fr.PeerByOverlayIP(p.OverlayIP)
	if peer == nil {
		return "not-configured"
	}
	return fmt.Sprintf("%s (rcv=%d snt=%d)", peer.State, peer.PfxRcvd, peer.PfxSent)
}

func printL2Section(cmd *cobra.Command, s *state.State) {
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "L2:")
	fmt.Fprintf(cmd.OutOrStdout(), "  %s on %s (vni=%d, dstport=%d, mtu=%d)\n",
		s.L2.VxlanIface, s.L2.BridgeIface, s.L2.VNI, s.L2.Port, s.L2.MTU)
	if len(s.L2.BridgeAddrs) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Bridge addrs: %s\n", strings.Join(s.L2.BridgeAddrs, ", "))
	}
	if len(s.L2.LocalPorts) > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), "  Local ports: %s\n", strings.Join(s.L2.LocalPorts, ", "))
	}
}

func printFRRSection(cmd *cobra.Command, fr *frr.Status) {
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintln(cmd.OutOrStdout(), "FRR / EVPN:")
	if !fr.Available {
		fmt.Fprintln(cmd.OutOrStdout(), "  (FRR not available — vtysh missing or BGP not running)")
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "  BGP router-id: %s (AS %d)\n", fr.RouterID, fr.LocalAS)
	if fr.VNI != nil {
		v := fr.VNI
		fmt.Fprintf(cmd.OutOrStdout(),
			"  VNI %d (%s): %d MACs, %d ARPs, %d remote VTEPs, advertise-svi-ip=%s\n",
			v.VNI, v.Type, v.NumMACs, v.NumARPs, v.NumRemoteVTEPs, v.AdvertiseSVIMacIP)
	}
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
