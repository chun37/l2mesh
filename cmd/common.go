package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/chun37/l2mesh/internal/wg"
	"github.com/spf13/cobra"
)

func runPeerAdd(cmd *cobra.Command, role state.Role, p state.Peer) error {
	return state.WithLock(statePath, func(s *state.State) error {
		if err := s.AddPeer(role, p); err != nil {
			return err
		}
		if err := applyToKernel(cmd, s.Node.Interface, p); err != nil {
			return err
		}
		syncFDBBestEffort(cmd, s)
		return nil
	})
}

func runPeerRemove(cmd *cobra.Command, role state.Role, name string) error {
	return state.WithLock(statePath, func(s *state.State) error {
		pubkey, err := s.RemovePeer(role, name)
		if err != nil {
			return err
		}
		if err := removeFromKernel(cmd, s.Node.Interface, pubkey); err != nil {
			return err
		}
		syncFDBBestEffort(cmd, s)
		return nil
	})
}

// If the WG interface is unavailable, log a warning and let the state change
// stand; the boot-time `l2mesh sync` will reconcile the kernel later.
func applyToKernel(cmd *cobra.Command, iface string, p state.Peer) error {
	client, err := wg.New(iface)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: WG iface %q unavailable, state saved only: %v\n", iface, err)
		return nil
	}
	defer client.Close()
	if err := client.AddOrUpdatePeer(p); err != nil {
		return fmt.Errorf("kernel apply: %w (state saved)", err)
	}
	return nil
}

func removeFromKernel(cmd *cobra.Command, iface, pubkey string) error {
	client, err := wg.New(iface)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: WG iface %q unavailable, state saved only: %v\n", iface, err)
		return nil
	}
	defer client.Close()
	if err := client.RemovePeer(pubkey); err != nil {
		return fmt.Errorf("kernel remove: %w (state saved)", err)
	}
	return nil
}
