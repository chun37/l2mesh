package cmd

import (
	"fmt"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/chun37/l2mesh/internal/l2"
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
		applyFRRBestEffort(cmd, s)
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
		applyFRRBestEffort(cmd, s)
		return nil
	})
}

// applyFRRBestEffort reapplies the FRR config after a peer change. Failures are
// reported as warnings so the state change still persists; the next
// `l2mesh sync` can retry.
func applyFRRBestEffort(cmd *cobra.Command, s *state.State) {
	if !frr.Installed() {
		return
	}
	if err := frr.Apply(s, nil); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: FRR reload failed: %v\n", err)
	}
}

// reconcileKernel pushes the in-memory state to the kernel (WG peers, L2
// interfaces) and FRR. BUM FDB is intentionally NOT touched here — that's
// owned by `l2mesh agent`, which reconciles every tick from the MST.
func reconcileKernel(cmd *cobra.Command, s *state.State) error {
	wgClient, err := wg.New(s.Node.Interface)
	if err != nil {
		return fmt.Errorf("wg new: %w", err)
	}
	defer wgClient.Close()
	if err := wgClient.Sync(s.FlatPeers()); err != nil {
		return fmt.Errorf("wg sync: %w", err)
	}
	if err := l2.Up(s); err != nil {
		return fmt.Errorf("l2 up: %w", err)
	}
	if frr.Installed() {
		if err := frr.Apply(s, nil); err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "warning: FRR apply failed: %v\n", err)
		}
	}
	return nil
}

// Kernel WG updates are best-effort: if they fail the state.json change still
// persists, and the next `l2mesh sync` will reconcile the kernel.
func applyToKernel(cmd *cobra.Command, iface string, p state.Peer) error {
	client, err := wg.New(iface)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: WG iface %q unavailable: %v\n", iface, err)
		return nil
	}
	defer client.Close()
	if err := client.AddOrUpdatePeer(p); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: kernel apply failed: %v\n", err)
	}
	return nil
}

func removeFromKernel(cmd *cobra.Command, iface, pubkey string) error {
	client, err := wg.New(iface)
	if err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: WG iface %q unavailable: %v\n", iface, err)
		return nil
	}
	defer client.Close()
	if err := client.RemovePeer(pubkey); err != nil {
		fmt.Fprintf(cmd.ErrOrStderr(), "warning: kernel remove failed: %v\n", err)
	}
	return nil
}
