package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
)

var (
	peerAddName    string
	peerAddPubkey  string
	peerAddIP      string
	peerRemoveName string
)

var peerCmd = &cobra.Command{
	Use:   "peer",
	Short: "Manage Leaf peers under this Root",
}

var peerAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add a Leaf peer (WireGuard peer; BUM entry will be added when VXLAN is enabled)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if peerAddName == "" || peerAddPubkey == "" || peerAddIP == "" {
			return fmt.Errorf("--name, --pubkey, --ip are required")
		}
		p := state.Peer{Name: peerAddName, PublicKey: peerAddPubkey, OverlayIP: peerAddIP}
		if err := runPeerAdd(cmd, state.RoleLeaf, p); err != nil {
			return err
		}
		fmt.Printf("added leaf %s (%s)\n", p.Name, p.OverlayIP)
		return nil
	},
}

var peerRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a Leaf peer",
	RunE: func(cmd *cobra.Command, args []string) error {
		if peerRemoveName == "" {
			return fmt.Errorf("--name is required")
		}
		if err := runPeerRemove(cmd, state.RoleLeaf, peerRemoveName); err != nil {
			return err
		}
		fmt.Printf("removed leaf %s\n", peerRemoveName)
		return nil
	},
}

var peerListCmd = &cobra.Command{
	Use:   "list",
	Short: "List all peers (roots + leafs)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tNAME\tOVERLAY\tENDPOINT\tPUBKEY")
		for _, p := range s.AllPeers() {
			ep := p.Endpoint
			if ep == "" {
				ep = "-"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", p.Kind, p.Name, p.OverlayIP, ep, p.PublicKey)
		}
		return tw.Flush()
	},
}

func init() {
	peerAddCmd.Flags().StringVar(&peerAddName, "name", "", "Leaf name (local label)")
	peerAddCmd.Flags().StringVar(&peerAddPubkey, "pubkey", "", "Leaf WireGuard public key")
	peerAddCmd.Flags().StringVar(&peerAddIP, "ip", "", "Leaf overlay IP (e.g. 100.64.0.10)")
	peerRemoveCmd.Flags().StringVar(&peerRemoveName, "name", "", "Leaf name to remove")
	peerCmd.AddCommand(peerAddCmd, peerRemoveCmd, peerListCmd)
	rootCmd.AddCommand(peerCmd)
}
