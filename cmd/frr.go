package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/spf13/cobra"
)

var frrCmd = &cobra.Command{
	Use:   "frr",
	Short: "FRR (BGP EVPN) inspection commands",
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

func init() {
	frrCmd.AddCommand(frrShowCmd)
	rootCmd.AddCommand(frrCmd)
}
