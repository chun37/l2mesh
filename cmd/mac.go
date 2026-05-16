package cmd

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/chun37/l2mesh/internal/frr"
	"github.com/spf13/cobra"
)

var macCmd = &cobra.Command{
	Use:   "mac",
	Short: "EVPN MAC table inspection",
}

var macListCmd = &cobra.Command{
	Use:   "list",
	Short: "List EVPN MACs (local + remote) with IPs and peer attribution",
	RunE: func(cmd *cobra.Command, args []string) error {
		s, err := loadState()
		if err != nil {
			return err
		}
		macs, err := frr.GetMACs(s.L2.VNI)
		if err != nil {
			return err
		}

		peerByVTEP := map[string]string{}
		for _, p := range s.Roots {
			peerByVTEP[p.OverlayIP] = p.Name
		}
		for _, p := range s.Leafs {
			peerByVTEP[p.OverlayIP] = p.Name
		}

		tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "MAC\tTYPE\tIPS\tVTEP/IFACE\tPEER")
		for _, m := range macs {
			ips := strings.Join(m.IPs, ", ")
			if ips == "" {
				ips = "-"
			}
			var loc, peer string
			if m.Type == "local" {
				loc = m.Interface
				peer = "(self)"
			} else {
				loc = m.RemoteVTEP
				if name, ok := peerByVTEP[m.RemoteVTEP]; ok {
					peer = name
				} else {
					peer = "(unknown)"
				}
			}
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", m.MAC, m.Type, ips, loc, peer)
		}
		return tw.Flush()
	},
}

func init() {
	macCmd.AddCommand(macListCmd)
	rootCmd.AddCommand(macCmd)
}
