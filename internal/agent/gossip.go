package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// GossipPort is the TCP port each agent listens on (over the overlay) for
// /info and /topology queries. Trusted: WG is the security boundary, no auth
// needed for the MVP.
const GossipPort = 4444

// Server is a tiny HTTP gossip endpoint.
type Server struct {
	selfFn     func() NodeInfo
	topologyFn func() map[string]NodeInfo
}

func NewServer(selfFn func() NodeInfo, topologyFn func() map[string]NodeInfo) *Server {
	return &Server{selfFn: selfFn, topologyFn: topologyFn}
}

// ListenAndServe binds on overlayIP:GossipPort and serves until ctx is done.
func (s *Server) ListenAndServe(ctx context.Context, overlayIP string) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/info", s.handleInfo)
	mux.HandleFunc("/topology", s.handleTopology)
	addr := joinHostPort(overlayIP, GossipPort)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 3 * time.Second}
	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("gossip server: %w", err)
	}
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.selfFn())
}

func (s *Server) handleTopology(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(s.topologyFn())
}

// FetchInfo asks the peer at overlayIP for its NodeInfo.
func FetchInfo(ctx context.Context, overlayIP string) (NodeInfo, error) {
	url := "http://" + joinHostPort(overlayIP, GossipPort) + "/info"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return NodeInfo{}, err
	}
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return NodeInfo{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return NodeInfo{}, fmt.Errorf("http %d", resp.StatusCode)
	}
	var info NodeInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		return NodeInfo{}, err
	}
	return info, nil
}

func joinHostPort(host string, port int) string {
	if strings.Contains(host, ":") {
		return net.JoinHostPort(host, fmt.Sprint(port))
	}
	return fmt.Sprintf("%s:%d", host, port)
}
