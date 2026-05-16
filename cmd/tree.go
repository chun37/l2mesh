package cmd

import (
	"fmt"
	"text/tabwriter"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
)

var (
	treeSetPeer string
	treeSetVal  string
)

var treeCmd = &cobra.Command{
	Use:   "tree",
	Short: "Inspect and edit the BUM forwarding tree (Phase 1: static config)",
	Long: `Each peer carries a tree_neighbor flag (default true). Peers with
tree_neighbor=false are NOT included in the local BUM list, so the operator can
build a loop-free spanning tree across a 3+ Root mesh by flipping selected
peers off on each node.

Tree edges must be configured symmetrically on both sides — l2mesh can only
see this node's view; the helper below just makes the local edit ergonomic.`,
}

var treeShowCmd = &cobra.Command{
	Use:   "show",
	Short: "List which peers are in the local BUM tree",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "KIND\tNAME\tOVERLAY\tTREE")
		for _, p := range s.AllPeers() {
			v := "yes"
			if !p.IsTreeNeighbor() {
				v = "no"
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", p.Kind, p.Name, p.OverlayIP, v)
		}
		return tw.Flush()
	},
}

var treeSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set tree_neighbor for one peer (does not coordinate with the peer)",
	Long: `Set tree_neighbor=true|false on the named local peer entry. Remember
to flip the same flag on the peer's side too — otherwise the BUM tree is
asymmetric and frames will flow one way but not the other.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if treeSetPeer == "" || treeSetVal == "" {
			return fmt.Errorf("--peer and --neighbor are required")
		}
		var want bool
		switch treeSetVal {
		case "true", "yes", "y":
			want = true
		case "false", "no", "n":
			want = false
		default:
			return fmt.Errorf("--neighbor must be true|false (got %q)", treeSetVal)
		}
		return state.WithLock(statePath, func(s *state.State) error {
			p := findPeerByName(s, treeSetPeer)
			if p == nil {
				return fmt.Errorf("peer %q not found", treeSetPeer)
			}
			b := want
			p.TreeNeighbor = &b
			fmt.Printf("set %s.tree_neighbor = %v\n", treeSetPeer, want)
			fmt.Println("note: make sure the corresponding entry on the peer's side carries the same flag")
			return nil
		})
	},
}

// findPeerByName searches both Roots and Leafs for the given name and returns a
// pointer that lives in the slice (so mutations persist when the state is saved).
func findPeerByName(s *state.State, name string) *state.Peer {
	for i := range s.Roots {
		if s.Roots[i].Name == name {
			return &s.Roots[i]
		}
	}
	for i := range s.Leafs {
		if s.Leafs[i].Name == name {
			return &s.Leafs[i]
		}
	}
	return nil
}

func init() {
	treeSetCmd.Flags().StringVar(&treeSetPeer, "peer", "", "Peer name to update")
	treeSetCmd.Flags().StringVar(&treeSetVal, "neighbor", "", "true|false")
	treeCmd.AddCommand(treeShowCmd, treeSetCmd)
	rootCmd.AddCommand(treeCmd)
}
