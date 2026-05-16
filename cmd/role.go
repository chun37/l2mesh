package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/chun37/l2mesh/internal/wg"
	"github.com/spf13/cobra"
)

var promoteEndpoint string

var promoteCmd = &cobra.Command{
	Use:   "promote",
	Short: "Promote this node from Leaf to Root",
	Long: `Set node.role=root, update FRR config, and recreate VXLAN in nolearning
mode. Note that --endpoint must be set (either via flag or already in
state.json) since Roots need a public endpoint other peers can reach.

After promotion you must coordinate with the other Root admins to add this
node as a peer on their side (l2mesh root add).`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var addArgs string
		err := state.WithLock(statePath, func(s *state.State) error {
			if s.Node.Role == state.RoleRoot {
				return fmt.Errorf("already root")
			}
			s.Node.Role = state.RoleRoot
			if promoteEndpoint != "" {
				s.Node.Endpoint = promoteEndpoint
			}
			if s.Node.Endpoint == "" {
				return fmt.Errorf("endpoint is empty; pass --endpoint or set node.endpoint in state.json first")
			}
			if err := reconcileKernel(cmd, s); err != nil {
				return err
			}
			pubkey, err := localPubkey(s.Node.Interface)
			if err == nil {
				addArgs = fmt.Sprintf(
					"l2mesh root add --name %s --pubkey %s --endpoint %s --ip %s",
					s.Node.Name, pubkey, s.Node.Endpoint, s.Node.OverlayIP)
			}
			return nil
		})
		if err != nil {
			return err
		}
		fmt.Println("promoted to root")
		if addArgs != "" {
			fmt.Println("share this command with other Root admins:")
			fmt.Println("  " + addArgs)
		}
		return nil
	},
}

var demoteCmd = &cobra.Command{
	Use:   "demote",
	Short: "Demote this node from Root to Leaf",
	Long: `Set node.role=leaf, clear FRR BGP config, and recreate VXLAN in learning
mode. Refuses to run while leafs are still attached — migrate or remove
them with l2mesh peer remove first.

After demotion, ask each other Root admin to drop this node with
l2mesh root remove --name <this-name>.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		var name string
		err := state.WithLock(statePath, func(s *state.State) error {
			if s.Node.Role == state.RoleLeaf {
				return fmt.Errorf("already leaf")
			}
			if len(s.Leafs) > 0 {
				return fmt.Errorf("cannot demote: %d leaf peer(s) configured; remove or migrate them first", len(s.Leafs))
			}
			s.Node.Role = state.RoleLeaf
			name = s.Node.Name
			return reconcileKernel(cmd, s)
		})
		if err != nil {
			return err
		}
		fmt.Println("demoted to leaf")
		fmt.Printf("ask other Root admins to run: l2mesh root remove --name %s\n", name)
		return nil
	},
}

// localPubkey returns the WireGuard public key of the named interface.
func localPubkey(iface string) (string, error) {
	c, err := wg.New(iface)
	if err != nil {
		return "", err
	}
	defer c.Close()
	dev, err := c.Device()
	if err != nil {
		return "", err
	}
	return dev.PublicKey.String(), nil
}

func init() {
	promoteCmd.Flags().StringVar(&promoteEndpoint, "endpoint", "", "Public endpoint host:port (v6: [addr]:port). Overrides node.endpoint in state.json.")
	rootCmd.AddCommand(promoteCmd, demoteCmd)
}
