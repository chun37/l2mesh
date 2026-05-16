// Package frr generates FRR config from l2mesh state and applies it via
// frr-reload.py. The agent owns the file at ConfigPath; FRR daemons are
// expected to be installed and running already (NixOS / distro responsibility).
package frr

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"text/template"

	"github.com/chun37/l2mesh/internal/state"
)

const ConfigPath = "/var/lib/l2mesh/frr.conf"

// Single template for all roles (Plan B). Root vs Leaf differs only by which
// slices populate neighbor blocks: a Root has other Roots in .Roots (regular
// iBGP) and its downstream Leaves in .Leafs (RR clients); a Leaf has its
// upstream Roots in .Roots and .Leafs is empty.
//
// BFD profile l2mesh = 300ms tx/rx with multiplier 3 (≈1s failure detect).
//
// Type-3 (BUM) filtering: agent rewrites the MST_VTEPS prefix-list on each
// MST change. route-map MST_T3 then accepts Type-3 routes only when the BGP
// next-hop is in MST_VTEPS, so the kernel's vxlan BUM list (driven by FRR
// from accepted Type-3 routes) follows the MST and 3+ Root meshes don't loop.
// Phase 2b BUM tree filter (partial): for each BGP peer that is NOT in our
// local MST, apply route-map BLOCK_T3 inbound. This blocks Type-3 routes
// arriving directly from non-MST peers. Routes reflected by MST peers (with
// NH rewritten via next-hop-self force) still pass — those convey BUM
// destinations for nodes we can't reach directly anyway.
//
// Caveat: this prevents redundant Type-3 from non-MST direct peers, but the
// FDB entry that zebra installs for a reflected Type-3 still uses the original
// originator's VTEP IP as the dst (from the EVPN NLRI, not the BGP next-hop).
// For a 3+ Root mesh that wants BUM transit through an intermediate Root, the
// VTEP also has to be reachable via the underlay — i.e., the originator's
// overlay IP must be in some WG peer's AllowedIPs. With our current /32-only
// AllowedIPs and no transit catch-all that holds for the 2-Root + Leaves
// topology but not yet for arbitrary 3+ Root meshes. See README "制約".
const configTmpl = `frr defaults datacenter
hostname {{.Node.Name}}
!
bfd
 profile l2mesh
  receive-interval 300
  transmit-interval 300
  detect-multiplier 3
 exit
exit
!
route-map BLOCK_T3 deny 10
 match evpn route-type multicast
route-map BLOCK_T3 permit 100
!
router bgp {{.Node.ASN}}
 bgp router-id {{.Node.OverlayIP}}
 no bgp default ipv4-unicast
{{- range .Roots}}
 neighbor {{.OverlayIP}} remote-as {{$.Node.ASN}}
 neighbor {{.OverlayIP}} update-source {{$.Node.OverlayIP}}
 neighbor {{.OverlayIP}} bfd profile l2mesh
{{- end}}
{{- range .Leafs}}
 neighbor {{.OverlayIP}} remote-as {{$.Node.ASN}}
 neighbor {{.OverlayIP}} update-source {{$.Node.OverlayIP}}
 neighbor {{.OverlayIP}} bfd profile l2mesh
{{- end}}
 !
 address-family l2vpn evpn
{{- range .Roots}}
  neighbor {{.OverlayIP}} activate
  neighbor {{.OverlayIP}} next-hop-self force
{{- if not (inMST .OverlayIP $.MSTNeighbors)}}
  neighbor {{.OverlayIP}} route-map BLOCK_T3 in
{{- end}}
{{- end}}
{{- range .Leafs}}
  neighbor {{.OverlayIP}} activate
  neighbor {{.OverlayIP}} route-reflector-client
  neighbor {{.OverlayIP}} next-hop-self force
{{- if not (inMST .OverlayIP $.MSTNeighbors)}}
  neighbor {{.OverlayIP}} route-map BLOCK_T3 in
{{- end}}
{{- end}}
  advertise-all-vni
  vni {{.L2.VNI}}
   advertise-svi-ip
  exit-vni
 exit-address-family
exit
!
`

var tmpl = template.Must(template.New("frr").Funcs(template.FuncMap{
	"inMST": func(ip string, mst []string) bool {
		for _, m := range mst {
			if m == ip {
				return true
			}
		}
		return false
	},
}).Parse(configTmpl))

// configData wraps state.State with the dynamic MST_VTEPS list. We can't add
// it to State directly because state.json doesn't carry MST info — it's
// derived on the fly by the agent and passed through to the template.
type configData struct {
	*state.State
	MSTNeighbors []string
}

// GenerateConfig renders the integrated FRR config for the given state.
//
// mstNeighbors is the list of overlay IPs the local node treats as its
// neighbors in the BUM spanning tree. When nil, fall back to all configured
// peers (fail-open) — used by `l2mesh sync` and by the agent's first tick
// before it has discovered the topology.
func GenerateConfig(s *state.State, mstNeighbors []string) (string, error) {
	if mstNeighbors == nil {
		for _, p := range s.AllPeers() {
			mstNeighbors = append(mstNeighbors, p.OverlayIP)
		}
	}
	data := configData{State: s, MSTNeighbors: mstNeighbors}
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("frr template: %w", err)
	}
	return buf.String(), nil
}

// Apply writes the generated config to ConfigPath and invokes frr-reload.py to
// diff-apply it against the running FRR config.
func Apply(s *state.State, mstNeighbors []string) error {
	if !Installed() {
		return nil
	}
	cfg, err := GenerateConfig(s, mstNeighbors)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(ConfigPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(ConfigPath, []byte(cfg), 0o644); err != nil {
		return fmt.Errorf("write frr config: %w", err)
	}
	return reload()
}

func reload() error {
	bin, err := findReloadBin()
	if err != nil {
		return err
	}
	args := []string{"--reload", "--stdout", ConfigPath}
	if dir, err := vtyshBindir(); err == nil {
		args = append([]string{"--bindir", dir}, args...)
	}
	cmd := exec.Command(bin, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("frr-reload: %w: %s", err, bytes.TrimSpace(out))
	}
	return nil
}

// vtyshBindir returns the directory containing vtysh, so frr-reload.py can
// call it instead of defaulting to /usr/bin (which doesn't exist on NixOS).
func vtyshBindir() (string, error) {
	vtysh, err := exec.LookPath("vtysh")
	if err != nil {
		return "", err
	}
	return filepath.Dir(vtysh), nil
}

func findReloadBin() (string, error) {
	if p, err := exec.LookPath("frr-reload.py"); err == nil {
		return p, nil
	}
	// Resolve vtysh through symlinks to find the FRR package root; on NixOS
	// libexec/ is not exposed in /run/current-system/sw/ so we walk back from
	// the resolved nix-store binary path.
	if vtysh, err := exec.LookPath("vtysh"); err == nil {
		resolved, err := filepath.EvalSymlinks(vtysh)
		if err == nil {
			base := filepath.Dir(filepath.Dir(resolved))
			for _, sub := range []string{"libexec/frr/frr-reload.py", "lib/frr/frr-reload.py"} {
				p := filepath.Join(base, sub)
				if _, err := os.Stat(p); err == nil {
					return p, nil
				}
			}
		}
	}
	for _, p := range []string{
		"/usr/lib/frr/frr-reload.py",
		"/usr/libexec/frr/frr-reload.py",
		"/usr/local/lib/frr/frr-reload.py",
	} {
		if _, err := os.Stat(p); err == nil {
			return p, nil
		}
	}
	return "", fmt.Errorf("frr-reload.py not found")
}

// Installed reports whether FRR appears to be available on this host (vtysh in PATH).
func Installed() bool {
	_, err := exec.LookPath("vtysh")
	return err == nil
}
