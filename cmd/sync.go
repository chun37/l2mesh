package cmd

import (
	"fmt"

	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Reconcile kernel WireGuard peers, VXLAN/bridge, BUM entries, and FRR config from state.json",
	Long:  "Used at boot via systemd to make the kernel and FRR state authoritative against state.json.",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		if err := reconcileKernel(cmd, s); err != nil {
			return err
		}
		fmt.Printf("synced role=%s, %d peers on %s, %s/%s up (BUM managed by l2mesh-agent)\n",
			s.Node.Role, len(s.FlatPeers()), s.Node.Interface,
			s.L2.VxlanIface, s.L2.BridgeIface)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
