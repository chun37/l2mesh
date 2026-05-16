package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
)

var frrCmd = &cobra.Command{
	Use:   "frr",
	Short: "FRR (BGP EVPN) integration commands",
}

var frrShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Print the FRR config generated from state.json (does not write or reload)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		cfg, err := frr.GenerateConfig(s)
		if err != nil {
			return err
		}
		fmt.Print(cfg)
		return nil
	},
}

var frrApplyCmd = &cobra.Command{
	Use:   "apply",
	Short: "Write FRR config and trigger frr-reload.py (no-op on Leaf nodes)",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		if s.Node.Role != state.RoleRoot {
			fmt.Println("node role is leaf; FRR is not managed on Leaf nodes")
			return nil
		}
		if !frr.Installed() {
			return fmt.Errorf("vtysh not in PATH; is FRR installed?")
		}
		if err := frr.Apply(s); err != nil {
			return err
		}
		fmt.Printf("frr config written to %s and reloaded\n", frr.ConfigPath)
		return nil
	},
}

func init() {
	frrCmd.AddCommand(frrShowCmd, frrApplyCmd)
	rootCmd.AddCommand(frrCmd)
}
