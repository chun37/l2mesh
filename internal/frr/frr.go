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
{{- end}}
{{- range .Leafs}}
  neighbor {{.OverlayIP}} activate
  neighbor {{.OverlayIP}} route-reflector-client
{{- end}}
  advertise-all-vni
  vni {{.L2.VNI}}
   advertise-svi-ip
  exit-vni
 exit-address-family
exit
!
`

var tmpl = template.Must(template.New("frr").Parse(configTmpl))

// GenerateConfig renders the integrated FRR config for the given state.
// Root vs Leaf is implicit in the slices: Roots fill .Roots and .Leafs;
// Leaves fill only .Roots (with their upstream Roots).
func GenerateConfig(s *state.State) (string, error) {
	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, s); err != nil {
		return "", fmt.Errorf("frr template: %w", err)
	}
	return buf.String(), nil
}

// Apply writes the generated config to ConfigPath and invokes frr-reload.py to
// diff-apply it against the running FRR config. On Leaf nodes it writes the
// minimal stub so demote cleans up any inherited BGP config.
func Apply(s *state.State) error {
	if !Installed() {
		return nil
	}
	cfg, err := GenerateConfig(s)
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
