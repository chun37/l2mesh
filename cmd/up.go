package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/l2"
	"github.com/spf13/cobra"
)

var upCmd = &cobra.Command{
	Use:   "up",
	Short: "Bring up the VXLAN + bridge data plane (BUM FDB is managed by l2mesh agent)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		if err := l2.Up(s); err != nil {
			return err
		}
		fmt.Printf("up: %s on %s (vni=%d, port=%d)\n",
			s.L2.VxlanIface, s.L2.BridgeIface, s.L2.VNI, s.L2.Port)
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

// BUM FDB is now owned exclusively by `l2mesh agent`. peer add / sync etc.
// don't touch FDB; the agent's next tick (≤5s) installs the correct entries
// from the MST.

func init() {
	rootCmd.AddCommand(upCmd, downCmd)
}
