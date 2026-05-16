package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/chun37/l2mesh/internal/agent"
	"github.com/spf13/cobra"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Run the l2mesh agent (gossip + auto MST + BUM FDB management)",
	Long: `Long-running daemon that gossips with directly-configured peers,
maintains a local view of the overlay graph, computes a minimum spanning tree
on every tick, and rewrites the vxlan BUM FDB to only the local MST edges.

While the agent is running it owns BUM forwarding; tree_neighbor flags in
state.json are ignored. Stop the agent to fall back to the static Phase 1
config.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		a := agent.New(statePath, agent.WithLogOutput(os.Stderr))
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		return a.Run(ctx)
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)
}
