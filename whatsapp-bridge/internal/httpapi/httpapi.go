// Package httpapi serves MCP (/mcp) and the REST API (/api) only on
// localhost and the Tailscale addresses, and only to callers that present
// the access token.
package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/version"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

var tailscaleNets = mustNets("100.64.0.0/10", "fd7a:115c:a1e0::/48")

func mustNets(cidrs ...string) []*net.IPNet {
	var out []*net.IPNet
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			panic(err)
		}
		out = append(out, n)
	}
	return out
}

func inNets(ip net.IP, nets []*net.IPNet) bool {
	for _, n := range nets {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}

// TailscaleIPs returns this machine's Tailscale addresses.
func TailscaleIPs() []net.IP {
	ifaces, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []net.IP
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			var ip net.IP
			switch v := a.(type) {
			case *net.IPNet:
				ip = v.IP
			case *net.IPAddr:
				ip = v.IP
			}
			if ip != nil && inNets(ip, tailscaleNets) {
				out = append(out, ip)
			}
		}
	}
	return out
}

// Server is the HTTP front door.
type Server struct {
	cfg     *config.Config
	allowed []*net.IPNet
	handler http.Handler
	log     waLog.Logger

	mu        sync.Mutex
	listening map[string]*http.Server
}

// New builds the server.
// panelHandler (may be nil) serves /panel/ with its own login instead of
// the token.
func New(cfg *config.Config, mcpServer *mcp.Server, bridge *wa.Bridge, pol *policy.Policy, panelHandler http.Handler, log waLog.Logger) (*Server, error) {
	allowed := mustNets(cfg.AllowedNetworks...)
	mux := http.NewServeMux()
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{SessionTimeout: 2 * time.Hour})
	mux.Handle("/mcp", mcpHandler)
	mux.Handle("/mcp/", mcpHandler)
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		st := bridge.Status()
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "version": version.Version,
			"linked": st.Paired, "connected": st.Connected && st.LoggedIn, "number": st.Number(),
			"summary": st.Summary(), "whatsapp": st})
	})
	if cfg.RESTEnabled {
		mux.HandleFunc("/api/send", restSend(bridge))
		mux.HandleFunc("/api/download", restDownload(bridge))
	}
	s := &Server{cfg: cfg, allowed: allowed, log: log, listening: map[string]*http.Server{}}
	s.handler = s.guard(mux, panelHandler)
	return s, nil
}

// guard rejects callers outside the allowed networks and without the token.
// The panel (/panel/) has its own login and is passed through after the
// network check.
func (s *Server) guard(next, panelHandler http.Handler) http.Handler {
	token := []byte(s.cfg.AuthToken)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		ip := net.ParseIP(host)
		if err != nil || ip == nil || !inNets(ip, s.allowed) {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		if panelHandler != nil && (r.URL.Path == "/panel" || strings.HasPrefix(r.URL.Path, "/panel/")) {
			panelHandler.ServeHTTP(w, r)
			return
		}
		if panelHandler != nil && r.URL.Path == "/" && r.Method == http.MethodGet {
			http.Redirect(w, r, "/panel/", http.StatusFound)
			return
		}
		got := ""
		if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
			got = strings.TrimSpace(a[7:])
		} else if k := r.Header.Get("X-API-Key"); k != "" {
			got = strings.TrimSpace(k)
		}
		if subtle.ConstantTimeCompare([]byte(got), token) != 1 {
			time.Sleep(300 * time.Millisecond) // slow down guessing
			w.Header().Set("WWW-Authenticate", `Bearer realm="whatsapp-mcp"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Handler returns the guarded handler (used by tests).
func (s *Server) Handler() http.Handler { return s.handler }

func (s *Server) wantedAddrs() []string {
	var out []string
	port := strconv.Itoa(s.cfg.ListenPort)
	for _, h := range s.cfg.ListenHosts {
		if strings.EqualFold(strings.TrimSpace(h), "tailscale") {
			for _, ip := range TailscaleIPs() {
				out = append(out, net.JoinHostPort(ip.String(), port))
			}
			continue
		}
		out = append(out, net.JoinHostPort(strings.TrimSpace(h), port))
	}
	return out
}

func (s *Server) listen(addr string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.listening[addr]; ok {
		return
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		s.log.Warnf("cannot listen on %s: %v", addr, err)
		return
	}
	srv := &http.Server{Handler: s.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 5 * time.Minute}
	s.listening[addr] = srv
	s.log.Infof("Listening on http://%s (MCP at /mcp)", addr)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			s.log.Errorf("server on %s stopped: %v", addr, err)
		}
		s.mu.Lock()
		delete(s.listening, addr)
		s.mu.Unlock()
	}()
}

// Run binds the configured addresses and keeps checking for Tailscale
// addresses that appear later (Tailscale may start after this service).
func (s *Server) Run(ctx context.Context) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		for _, a := range s.wantedAddrs() {
			s.listen(a)
		}
		select {
		case <-ctx.Done():
			s.mu.Lock()
			for _, srv := range s.listening {
				c, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = srv.Shutdown(c)
				cancel()
			}
			s.mu.Unlock()
			return
		case <-tick.C:
		}
	}
}

// Addresses returns what is currently bound.
func (s *Server) Addresses() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for a := range s.listening {
		out = append(out, a)
	}
	return out
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

type sendReq struct {
	Recipient string `json:"recipient"`
	Message   string `json:"message"`
	MediaPath string `json:"media_path,omitempty"`
	VoiceNote bool   `json:"voice_note,omitempty"`
}

func restSend(b *wa.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req sendReq
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "invalid request"})
			return
		}
		res, err := b.Send(r.Context(), wa.SendRequest{Recipient: req.Recipient, Text: req.Message, MediaPath: req.MediaPath, VoiceNote: req.VoiceNote}, policy.ActorREST)
		if err != nil {
			code := http.StatusInternalServerError
			var d *policy.ErrDenied
			if errors.As(err, &d) {
				code = http.StatusForbidden
			}
			writeJSON(w, code, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "message": fmt.Sprintf("Message sent to %s", res.Recipient), "message_id": res.MessageID})
	}
}

func restDownload(b *wa.Bridge) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			MessageID string `json:"message_id"`
			ChatJID   string `json:"chat_jid"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<16)).Decode(&req); err != nil || req.MessageID == "" || req.ChatJID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"success": false, "message": "message_id and chat_jid are required"})
			return
		}
		d, err := b.Download(r.Context(), req.MessageID, req.ChatJID, policy.ActorREST)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"success": false, "message": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"success": true, "filename": d.Filename, "path": d.Path, "media_type": d.MediaType})
	}
}
