package cmd

import (
	"fmt"
	"text/tabwriter"
	"time"

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

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tNAME\tOVERLAY\tENDPOINT\tHANDSHAKE\tSTATUS")
		for _, p := range s.AllPeers() {
			key, err := wgtypes.ParseKey(p.PublicKey)
			handshake := "-"
			status := "unknown"
			if err == nil {
				if kp, ok := live[key]; ok {
					if kp.LastHandshakeTime.IsZero() {
						handshake = "never"
						status = "pending"
					} else {
						age := time.Since(kp.LastHandshakeTime).Round(time.Second)
						handshake = age.String() + " ago"
						if age < 3*time.Minute {
							status = "alive"
						} else {
							status = "stale"
						}
					}
				} else {
					status = "missing-in-kernel"
				}
			} else {
				status = "bad-pubkey"
			}
			ep := p.Endpoint
			if ep == "" {
				ep = "(dynamic)"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
				p.Kind, p.Name, p.OverlayIP, ep, handshake, status)
		}
		return tw.Flush()
	},
}

func init() {
	rootCmd.AddCommand(statusCmd)
}
