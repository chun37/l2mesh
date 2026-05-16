package cmd

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/chun37/l2mesh/internal/state"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	initName       string
	initRole       string
	initOverlayIP  string
	initEndpoint   string
	initInterface  string
	initASN        uint32
	initListenPort int
	initForce      bool
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Create a fresh state.json with this node's identity",
	Long: `Initialize /var/lib/l2mesh/state.json (or --state PATH) with the local
node's identity. Missing flags are prompted for when stdin is a TTY;
otherwise the command errors out so it stays scriptable.

L2 (VXLAN / bridge) defaults are written without prompting; edit the
file directly to override.`,
	RunE: runInit,
}

func runInit(cmd *cobra.Command, args []string) error {
	if _, err := os.Stat(statePath); err == nil {
		if !initForce {
			return fmt.Errorf("state file %s already exists; pass --force to overwrite", statePath)
		}
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("stat state: %w", err)
	}

	interactive := term.IsTerminal(int(os.Stdin.Fd()))
	in := bufio.NewReader(os.Stdin)
	out := cmd.OutOrStdout()

	name, err := requiredField(in, out, interactive, "name", initName, "")
	if err != nil {
		return err
	}

	roleStr, err := requiredField(in, out, interactive, "role (root|leaf)", initRole, "root")
	if err != nil {
		return err
	}
	var role state.Role
	switch roleStr {
	case "root":
		role = state.RoleRoot
	case "leaf":
		role = state.RoleLeaf
	default:
		return fmt.Errorf("invalid role %q (expected root or leaf)", roleStr)
	}

	overlayIP, err := requiredField(in, out, interactive, "overlay_ip (e.g. 100.64.0.1)", initOverlayIP, "")
	if err != nil {
		return err
	}
	if ip := net.ParseIP(overlayIP); ip == nil || ip.To4() == nil {
		return fmt.Errorf("invalid overlay IPv4 %q", overlayIP)
	}

	endpoint := initEndpoint
	if role == state.RoleRoot {
		endpoint, err = requiredField(in, out, interactive, "endpoint (host:port; v6 = [addr]:port)", initEndpoint, "")
		if err != nil {
			return err
		}
	}

	iface, err := requiredField(in, out, interactive, "wireguard interface", initInterface, "wg-l2mesh")
	if err != nil {
		return err
	}

	asnStr, err := requiredField(in, out, interactive, "BGP ASN", uintToStrIfSet(initASN), "65000")
	if err != nil {
		return err
	}
	asn, err := strconv.ParseUint(asnStr, 10, 32)
	if err != nil {
		return fmt.Errorf("invalid asn %q: %w", asnStr, err)
	}

	listenStr, err := requiredField(in, out, interactive, "wireguard listen port", intToStrIfSet(initListenPort), "51820")
	if err != nil {
		return err
	}
	listen, err := strconv.Atoi(listenStr)
	if err != nil {
		return fmt.Errorf("invalid listen-port %q: %w", listenStr, err)
	}

	s := state.Default()
	s.Node.Name = name
	s.Node.Role = role
	s.Node.OverlayIP = overlayIP
	s.Node.Endpoint = endpoint
	s.Node.Interface = iface
	s.Node.ASN = uint32(asn)
	s.Node.ListenPort = listen

	if err := s.Save(statePath); err != nil {
		return err
	}
	fmt.Fprintf(out, "wrote %s\n", statePath)
	fmt.Fprintf(out, "  name=%s role=%s overlay_ip=%s endpoint=%q interface=%s asn=%d\n",
		name, role, overlayIP, endpoint, iface, asn)
	return nil
}

func requiredField(in *bufio.Reader, out io.Writer, interactive bool, label, flagVal, def string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if !interactive {
		if def != "" {
			return def, nil
		}
		return "", fmt.Errorf("missing required value for %s (pass via flag, or run in a TTY for prompts)", label)
	}
	v, err := promptLine(in, out, label, def)
	if err != nil {
		return "", err
	}
	if v == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	return v, nil
}

func promptLine(in *bufio.Reader, out io.Writer, label, def string) (string, error) {
	if def != "" {
		fmt.Fprintf(out, "%s [%s]: ", label, def)
	} else {
		fmt.Fprintf(out, "%s: ", label)
	}
	line, err := in.ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return def, nil
	}
	return line, nil
}

func uintToStrIfSet(v uint32) string {
	if v == 0 {
		return ""
	}
	return strconv.FormatUint(uint64(v), 10)
}

func intToStrIfSet(v int) string {
	if v == 0 {
		return ""
	}
	return strconv.Itoa(v)
}

func init() {
	initCmd.Flags().StringVar(&initName, "name", "", "Node name (label)")
	initCmd.Flags().StringVar(&initRole, "role", "", "Node role: root or leaf (default root)")
	initCmd.Flags().StringVar(&initOverlayIP, "overlay-ip", "", "Overlay IPv4 (e.g. 100.64.0.1)")
	initCmd.Flags().StringVar(&initEndpoint, "endpoint", "", "Public endpoint host:port (v6: [addr]:port). Required for root.")
	initCmd.Flags().StringVar(&initInterface, "interface", "", "WireGuard interface name (default wg-l2mesh)")
	initCmd.Flags().Uint32Var(&initASN, "asn", 0, "BGP ASN (default 65000)")
	initCmd.Flags().IntVar(&initListenPort, "listen-port", 0, "WireGuard listen port (default 51820)")
	initCmd.Flags().BoolVar(&initForce, "force", false, "Overwrite existing state.json")
	rootCmd.AddCommand(initCmd)
}
