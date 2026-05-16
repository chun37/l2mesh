package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/wg"
	"github.com/spf13/cobra"
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Replace kernel WireGuard peers with the set from state.json",
	Long:  "Used at boot via systemd to restore the configured peer set into the kernel.",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		client, err := wg.New(s.Node.Interface)
		if err != nil {
			return err
		}
		defer client.Close()
		peers := s.FlatPeers()
		if err := client.Sync(peers); err != nil {
			return fmt.Errorf("sync: %w", err)
		}
		fmt.Printf("synced %d peers to %s\n", len(peers), s.Node.Interface)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(syncCmd)
}
