package cmd

import (
	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
)

var statePath string

var rootCmd = &cobra.Command{
	Use:   "l2mesh",
	Short: "L2 Mesh VPN agent CLI",
	Long:  "Manage WireGuard peers, VXLAN, and EVPN for the L2 Mesh VPN.",
}

func Execute() error {
	return rootCmd.Execute()
}

func init() {
	rootCmd.PersistentFlags().StringVar(&statePath, "state", state.DefaultPath, "Path to state.json")
}

func loadState() (*state.State, error) {
	return state.Load(statePath)
}

func saveState(s *state.State) error {
	return s.Save(statePath)
}
