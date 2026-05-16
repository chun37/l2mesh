package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
)

var (
	rootAddName     string
	rootAddPubkey   string
	rootAddEndpoint string
	rootAddIP       string
	rootRemoveName  string
)

var rootPeerCmd = &cobra.Command{
	Use:   "root",
	Short: "Manage Root-to-Root peers",
}

var rootAddCmd = &cobra.Command{
	Use:   "add",
	Short: "Add another Root (WireGuard peer; FRR BGP neighbor will be added when FRR is enabled)",
	RunE: func(cmd *cobra.Command, args []string) error {
		if rootAddName == "" || rootAddPubkey == "" || rootAddEndpoint == "" || rootAddIP == "" {
			return fmt.Errorf("--name, --pubkey, --endpoint, --ip are required")
		}
		p := state.Peer{
			Name:      rootAddName,
			PublicKey: rootAddPubkey,
			OverlayIP: rootAddIP,
			Endpoint:  rootAddEndpoint,
		}
		if err := runPeerAdd(cmd, state.RoleRoot, p); err != nil {
			return err
		}
		fmt.Printf("added root %s (%s, endpoint=%s)\n", p.Name, p.OverlayIP, p.Endpoint)
		fmt.Println("note: BGP EVPN neighbor must still be configured in FRR once available")
		return nil
	},
}

var rootRemoveCmd = &cobra.Command{
	Use:   "remove",
	Short: "Remove a Root peer",
	RunE: func(cmd *cobra.Command, args []string) error {
		if rootRemoveName == "" {
			return fmt.Errorf("--name is required")
		}
		if err := runPeerRemove(cmd, state.RoleRoot, rootRemoveName); err != nil {
			return err
		}
		fmt.Printf("removed root %s\n", rootRemoveName)
		return nil
	},
}

func init() {
	rootAddCmd.Flags().StringVar(&rootAddName, "name", "", "Root name (local label)")
	rootAddCmd.Flags().StringVar(&rootAddPubkey, "pubkey", "", "Root WireGuard public key")
	rootAddCmd.Flags().StringVar(&rootAddEndpoint, "endpoint", "", "Root endpoint (host:port, IPv6 use [addr]:port)")
	rootAddCmd.Flags().StringVar(&rootAddIP, "ip", "", "Root overlay IP (e.g. 100.64.0.2)")
	rootRemoveCmd.Flags().StringVar(&rootRemoveName, "name", "", "Root name to remove")
	rootPeerCmd.AddCommand(rootAddCmd, rootRemoveCmd)
	rootCmd.AddCommand(rootPeerCmd)
}
