package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/panel"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// Cloud serves MCP, and nothing else, to Claude in the cloud. It listens
// only on 127.0.0.1:CloudPort; Tailscale Funnel publishes that port on the
// internet. Every request needs the cloud key, and nothing is answered
// while the cloud_access_enabled setting is off.
type Cloud struct {
	cfg *config.Config
	pol *policy.Policy
	mcp http.Handler
	log waLog.Logger

	mu        sync.Mutex
	key       string
	listening bool
	listenErr string
	fails     []time.Time
	reqs      []time.Time
	lastDeny  time.Time

	dnsMu   sync.Mutex
	dnsName string
	dnsAt   time.Time
}

const (
	cloudMaxPerMinute = 240 // requests per minute, all callers together
	cloudMaxFails     = 20  // wrong keys per 10 minutes before pausing
)

// NewCloud builds the cloud front door with its own MCP server.
func NewCloud(cfg *config.Config, mcpServer *mcp.Server, pol *policy.Policy, log waLog.Logger) *Cloud {
	h := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return mcpServer },
		&mcp.StreamableHTTPOptions{SessionTimeout: 2 * time.Hour})
	return &Cloud{cfg: cfg, pol: pol, mcp: h, log: log, key: cfg.CloudKey}
}

func (c *Cloud) addr() string { return net.JoinHostPort("127.0.0.1", strconv.Itoa(c.cfg.CloudPort)) }

// Run keeps the local listener up until ctx ends.
func (c *Cloud) Run(ctx context.Context) {
	if c.cfg.CloudPort == 0 {
		return
	}
	for {
		ln, err := net.Listen("tcp", c.addr())
		if err == nil {
			srv := &http.Server{Handler: c, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 5 * time.Minute}
			c.mu.Lock()
			c.listening, c.listenErr = true, ""
			c.mu.Unlock()
			c.log.Infof("Cloud access listener on http://%s (answers only when cloud_access_enabled is on)", c.addr())
			done := make(chan struct{})
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
					c.log.Errorf("cloud listener stopped: %v", err)
				}
				close(done)
			}()
			select {
			case <-ctx.Done():
				sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = srv.Shutdown(sc)
				cancel()
				return
			case <-done:
			}
		} else {
			c.log.Warnf("cannot listen on %s for cloud access: %v", c.addr(), err)
			c.mu.Lock()
			c.listenErr = err.Error()
			c.mu.Unlock()
		}
		c.mu.Lock()
		c.listening = false
		c.mu.Unlock()
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

func trim(ts []time.Time, window time.Duration) []time.Time {
	cut := time.Now().Add(-window)
	i := 0
	for i < len(ts) && ts[i].Before(cut) {
		i++
	}
	return ts[i:]
}

func cloudJSON(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": msg})
}

// ServeHTTP checks the switch, the rate and the key, then hands the call
// to MCP. Paths: /mcp/k/<cloud key> (for claude.ai connectors) or /mcp
// with "Authorization: Bearer <cloud key>".
func (c *Cloud) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	ctx := r.Context()
	s, err := c.pol.Settings(ctx)
	if err != nil || !s.CloudAccessEnabled {
		cloudJSON(w, http.StatusServiceUnavailable, "cloud access is turned off on the server")
		return
	}
	now := time.Now()
	c.mu.Lock()
	c.reqs = append(trim(c.reqs, time.Minute), now)
	c.fails = trim(c.fails, 10*time.Minute)
	tooMany := len(c.reqs) > cloudMaxPerMinute
	paused := len(c.fails) >= cloudMaxFails
	key := c.key
	c.mu.Unlock()
	if tooMany {
		w.Header().Set("Retry-After", "30")
		cloudJSON(w, http.StatusTooManyRequests, "too many requests, try again in a moment")
		return
	}
	if paused {
		w.Header().Set("Retry-After", "600")
		cloudJSON(w, http.StatusTooManyRequests, "too many wrong keys; paused for 10 minutes")
		return
	}
	got, rest := "", ""
	switch {
	case strings.HasPrefix(r.URL.Path, "/mcp/k/"):
		p := strings.TrimPrefix(r.URL.Path, "/mcp/k/")
		got, rest, _ = strings.Cut(p, "/")
		if rest != "" {
			rest = "/" + rest
		}
	case r.URL.Path == "/mcp" || strings.HasPrefix(r.URL.Path, "/mcp/"):
		if a := r.Header.Get("Authorization"); len(a) > 7 && strings.EqualFold(a[:7], "bearer ") {
			got = strings.TrimSpace(a[7:])
		}
		rest = strings.TrimPrefix(r.URL.Path, "/mcp")
	default:
		http.NotFound(w, r)
		return
	}
	if key == "" || subtle.ConstantTimeCompare([]byte(got), []byte(key)) != 1 {
		c.mu.Lock()
		c.fails = append(c.fails, now)
		audit := now.Sub(c.lastDeny) > time.Minute // at most one log line a minute
		if audit {
			c.lastDeny = now
		}
		c.mu.Unlock()
		if audit {
			c.pol.Audit(ctx, policy.ActorCloud, "cloud.denied", "", "denied", map[string]any{"from": r.Header.Get("X-Forwarded-For")})
		}
		time.Sleep(400 * time.Millisecond)
		cloudJSON(w, http.StatusUnauthorized, "unauthorized")
		return
	}
	r2 := r.Clone(ctx)
	r2.URL.Path = "/mcp" + rest
	r2.URL.RawPath = ""
	r2.Header.Del("Authorization")
	c.mcp.ServeHTTP(w, r2)
}

// RotateKey makes a new cloud key, saves it and returns it. The old
// address stops working at once.
func (c *Cloud) RotateKey() (string, error) {
	k, err := config.RandomSecret(24)
	if err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	old := c.cfg.CloudKey
	c.cfg.CloudKey = k
	if err := c.cfg.Save(c.cfg.Path()); err != nil {
		c.cfg.CloudKey = old
		return "", err
	}
	c.key = k
	return k, nil
}

// tailscaleBin finds the tailscale command line.
func tailscaleBin() string {
	if runtime.GOOS == "windows" {
		for _, env := range []string{"ProgramFiles", "ProgramW6432"} {
			if base := os.Getenv(env); base != "" {
				p := filepath.Join(base, "Tailscale", "tailscale.exe")
				if _, err := os.Stat(p); err == nil {
					return p
				}
			}
		}
	}
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p
	}
	if runtime.GOOS == "darwin" {
		p := "/Applications/Tailscale.app/Contents/MacOS/Tailscale"
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func runTailscale(ctx context.Context, args ...string) ([]byte, error) {
	bin := tailscaleBin()
	if bin == "" {
		return nil, errors.New("no se encontró Tailscale en este servidor")
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, bin, args...).CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return out, errors.New(msg)
	}
	return out, nil
}

// dns returns this machine's Tailscale name (machine.tailnet.ts.net).
func (c *Cloud) dns(ctx context.Context) string {
	c.dnsMu.Lock()
	defer c.dnsMu.Unlock()
	if c.dnsName != "" && time.Since(c.dnsAt) < 10*time.Minute {
		return c.dnsName
	}
	out, err := runTailscale(ctx, "status", "--json", "--peers=false")
	if err != nil {
		return c.dnsName
	}
	var st struct {
		Self struct {
			DNSName string `json:"DNSName"`
		} `json:"Self"`
	}
	if json.Unmarshal(out, &st) == nil && st.Self.DNSName != "" {
		c.dnsName = strings.TrimSuffix(st.Self.DNSName, ".")
		c.dnsAt = time.Now()
	}
	return c.dnsName
}

func (c *Cloud) funnelCmd() string {
	return fmt.Sprintf("tailscale funnel --bg --https=%d http://%s", c.cfg.CloudPublicPort, c.addr())
}

type serveStatus struct {
	AllowFunnel map[string]bool `json:"AllowFunnel"`
	Web         map[string]struct {
		Handlers map[string]struct {
			Proxy string `json:"Proxy"`
		} `json:"Handlers"`
	} `json:"Web"`
}

func (c *Cloud) serveStatus(ctx context.Context) (serveStatus, error) {
	var st serveStatus
	out, err := runTailscale(ctx, "funnel", "status", "--json")
	if err != nil {
		return st, err
	}
	if len(strings.TrimSpace(string(out))) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(out, &st); err != nil {
		return st, fmt.Errorf("respuesta de Tailscale no válida: %w", err)
	}
	return st, nil
}

func (c *Cloud) ours(proxy string) bool {
	p := strconv.Itoa(c.cfg.CloudPort)
	return strings.Contains(proxy, "127.0.0.1:"+p) || strings.Contains(proxy, "localhost:"+p)
}

// otherUse returns what else is published on our public port, if anything.
func (c *Cloud) otherUse(st serveStatus) string {
	suffix := ":" + strconv.Itoa(c.cfg.CloudPublicPort)
	for hp, web := range st.Web {
		if !strings.HasSuffix(hp, suffix) {
			continue
		}
		for path, h := range web.Handlers {
			if !c.ours(h.Proxy) {
				return path + " -> " + h.Proxy
			}
		}
	}
	return ""
}

// funnelOn says whether Funnel currently publishes our local port.
func (c *Cloud) funnelOn(ctx context.Context) (bool, error) {
	st, err := c.serveStatus(ctx)
	if err != nil {
		return false, err
	}
	suffix := ":" + strconv.Itoa(c.cfg.CloudPublicPort)
	for hp, on := range st.AllowFunnel {
		if !on || !strings.HasSuffix(hp, suffix) {
			continue
		}
		for _, h := range st.Web[hp].Handlers {
			if c.ours(h.Proxy) {
				return true, nil
			}
		}
	}
	return false, nil
}

// SetFunnel publishes (on) or withdraws (off) the cloud port with Funnel.
func (c *Cloud) SetFunnel(ctx context.Context, on bool) error {
	port := "--https=" + strconv.Itoa(c.cfg.CloudPublicPort)
	// Never take over a port another app (etiquetas, LeadsApp...) publishes.
	st, err := c.serveStatus(ctx)
	if err != nil {
		return err
	}
	if other := c.otherUse(st); other != "" {
		return fmt.Errorf("el puerto %d de Funnel ya lo usa otra cosa (%s); no lo toqué", c.cfg.CloudPublicPort, other)
	}
	if on {
		_, err := runTailscale(ctx, "funnel", "--bg", port, "http://"+c.addr())
		return err
	}
	_, err = runTailscale(ctx, "funnel", port, "off")
	return err
}

// Info describes the cloud access for the panel.
func (c *Cloud) Info(ctx context.Context) panel.CloudInfo {
	s, _ := c.pol.Settings(ctx)
	c.mu.Lock()
	info := panel.CloudInfo{Enabled: s.CloudAccessEnabled, Listening: c.listening, LocalAddr: c.addr(), FunnelCmd: c.funnelCmd()}
	key, lerr := c.key, c.listenErr
	c.mu.Unlock()
	if lerr != "" {
		info.Note = "No se pudo abrir el puerto local " + c.addr() + ": " + lerr
	}
	if dns := c.dns(ctx); dns != "" && key != "" {
		info.URL = fmt.Sprintf("https://%s:%d/mcp/k/%s", dns, c.cfg.CloudPublicPort, key)
	} else if info.Note == "" {
		info.Note = "No se pudo leer el nombre de Tailscale de este servidor."
	}
	if on, err := c.funnelOn(ctx); err == nil {
		info.Funnel = on
		info.FunnelKnown = true
		if st, err := c.serveStatus(ctx); err == nil && !on {
			if other := c.otherUse(st); other != "" {
				info.Note = fmt.Sprintf("Ojo: el puerto %d de Funnel ya lo usa otra cosa (%s).", c.cfg.CloudPublicPort, other)
			}
		}
	}
	return info
}
