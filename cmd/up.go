package cmd

import (
	"errors"
	"fmt"

	"github.com/chun37/l2mesh/internal/l2"
	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
	"github.com/vishvananda/netlink"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring up the VXLAN + bridge data plane and sync BUM entries",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		if err := l2.Up(s); err != nil {
			return err
		}
		if err := l2.SyncFDB(s, peerVTEPs(s)); err != nil {
			return err
		}
		fmt.Printf("up: %s on %s (vni=%d, port=%d, %d BUM peers)\n",
			s.L2.VxlanIface, s.L2.BridgeIface, s.L2.VNI, s.L2.Port, len(peerVTEPs(s)))
		return nil
	},
}

var downCmd = &cobra.Command{
	Use:   "down",
	Short: "Tear down the VXLAN + bridge data plane",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		if err := l2.Down(s); err != nil {
			return err
		}
		fmt.Printf("down: %s, %s removed\n", s.L2.VxlanIface, s.L2.BridgeIface)
		return nil
	},
}

func peerVTEPs(s *state.State) []string {
	out := make([]string, 0, len(s.Roots)+len(s.Leafs))
	for _, p := range s.Roots {
		out = append(out, p.OverlayIP)
	}
	for _, p := range s.Leafs {
		out = append(out, p.OverlayIP)
	}
	return out
}

// syncFDBBestEffort calls SyncFDB but tolerates a missing VXLAN interface, so
// that peer add/remove still succeeds before `l2mesh up` has been run.
func syncFDBBestEffort(cmd *cobra.Command, s *state.State) {
	if err := l2.SyncFDB(s, peerVTEPs(s)); err != nil {
		var lnf netlink.LinkNotFoundError
		if errors.As(err, &lnf) {
			return
		}
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: FDB sync failed: %v\n", err)
	}
}

func init() {
	rootCmd.AddCommand(upCmd, downCmd)
}
