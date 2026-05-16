package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/chun37/l2mesh/internal/l2"
	"github.com/chun37/l2mesh/internal/state"
	"github.com/chun37/l2mesh/internal/wg"
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

		wgClient, err := wg.New(s.Node.Interface)
		if err != nil {
			return err
		}
		defer wgClient.Close()
		peers := s.FlatPeers()
		if err := wgClient.Sync(peers); err != nil {
			return fmt.Errorf("wg sync: %w", err)
		}

		if err := l2.Up(s); err != nil {
			return fmt.Errorf("l2 up: %w", err)
		}
		if err := l2.SyncFDB(s, peerVTEPs(s)); err != nil {
			return fmt.Errorf("fdb sync: %w", err)
		}

		frrStatus := syncFRR(cmd, s)

		fmt.Printf("synced %d peers to %s; %s/%s up with %d BUM peers; FRR: %s\n",
			len(peers), s.Node.Interface,
			s.L2.VxlanIface, s.L2.BridgeIface, len(peerVTEPs(s)), frrStatus)
		return nil
	},
}

func syncFRR(cmd *cobra.Command, s *state.State) string {
	if s.Node.Role != state.RoleRoot {
		return "skipped (leaf)"
	}
	if !frr.Installed() {
		return "skipped (vtysh not found)"
	}
	if err := frr.Apply(s); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: FRR apply failed: %v\n", err)
		return "failed"
	}
	return "applied"
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
