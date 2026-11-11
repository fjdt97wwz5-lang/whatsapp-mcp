// Package tsnode runs WhatsApp MCP as its own small machine on the user's
// tailnet (Tailscale's tsnet), so Claude in the cloud can reach it through
// Funnel on the normal HTTPS port 443 under its own name, e.g.
// https://whatsapp-mcp.<tailnet>.ts.net, without touching the Tailscale
// already installed on the server or what it publishes (the label app).
//
// The node joins once with an auth key the user pastes in the panel. The
// key is used only for that first join and never saved; afterwards the
// node's own state (in Dir) keeps it signed in.
package tsnode

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
	"tailscale.com/envknob"
	"tailscale.com/tsnet"
)

// States shown in the panel.
const (
	Off           = "off"            // not started
	NeedsKey      = "needs_key"      // never joined, or Tailscale signed it out
	Starting      = "starting"       // joining / connecting
	NeedsApproval = "needs_approval" // waiting for approval in the admin console
	Running       = "running"        // published on Funnel :443
	Error         = "error"          // see Info.Error
)

// Info is what the panel shows.
type Info struct {
	State    string `json:"state"`
	DNSName  string `json:"dns_name,omitempty"`
	Error    string `json:"error,omitempty"`
	HasState bool   `json:"has_state"`
}

// Node is the embedded Tailscale machine.
type Node struct {
	dir       string
	handler   http.Handler
	log       waLog.Logger
	onRunning func(dnsName string)

	mu     sync.Mutex
	gen    int
	cancel context.CancelFunc
	done   chan struct{}
	info   Info
}

// New prepares the node. Nothing runs until Start.
func New(dir string, handler http.Handler, log waLog.Logger, onRunning func(string)) *Node {
	// Keep Tailscale's own diagnostic logs on this server instead of
	// uploading them to Tailscale.
	envknob.SetNoLogsNoSupport()
	return &Node{dir: dir, handler: handler, log: log, onRunning: onRunning, info: Info{State: Off}}
}

// joinedFile marks that the node got into the tailnet at least once (the
// Tailscale state file alone also exists after a failed join).
const joinedFile = "joined.ok"

// HasState reports whether the node already joined the tailnet once.
func (n *Node) HasState() bool {
	if _, err := os.Stat(filepath.Join(n.dir, "tailscaled.state")); err != nil {
		return false
	}
	_, err := os.Stat(filepath.Join(n.dir, joinedFile))
	return err == nil
}

// Info returns the current state.
func (n *Node) Info() Info {
	n.mu.Lock()
	defer n.mu.Unlock()
	i := n.info
	i.HasState = n.HasState()
	return i
}

func (n *Node) set(gen int, state, dns, msg string) bool {
	n.mu.Lock()
	defer n.mu.Unlock()
	if gen != n.gen {
		return false
	}
	n.info = Info{State: state, DNSName: dns, Error: msg}
	return true
}

// Start (re)starts the node. authKey may be empty when the node already
// joined before.
func (n *Node) Start(hostname, authKey string) {
	n.Stop()
	n.mu.Lock()
	n.gen++
	gen := n.gen
	ctx, cancel := context.WithCancel(context.Background())
	n.cancel = cancel
	done := make(chan struct{})
	n.done = done
	if authKey == "" && !n.HasState() {
		n.info = Info{State: NeedsKey}
		n.mu.Unlock()
		cancel()
		close(done)
		return
	}
	n.info = Info{State: Starting}
	n.mu.Unlock()
	go func() {
		defer close(done)
		n.run(ctx, gen, hostname, authKey)
	}()
}

// Stop shuts the node down (it stays registered in the tailnet).
func (n *Node) Stop() {
	n.mu.Lock()
	cancel, done := n.cancel, n.done
	n.cancel, n.done = nil, nil
	n.gen++
	n.info = Info{State: Off}
	n.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(15 * time.Second):
		}
	}
}

// Forget signs the node out of the tailnet and deletes its state, so a new
// auth key is needed next time.
func (n *Node) Forget(hostname string) error {
	n.Stop()
	if _, err := os.Stat(filepath.Join(n.dir, "tailscaled.state")); err == nil {
		srv := &tsnet.Server{Dir: n.dir, Hostname: hostname, Logf: func(string, ...any) {}, UserLogf: func(string, ...any) {}}
		if err := srv.Start(); err == nil {
			if lc, err := srv.LocalClient(); err == nil {
				ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
				_ = lc.Logout(ctx)
				cancel()
			}
			_ = srv.Close()
		}
	}
	return os.RemoveAll(n.dir)
}

func (n *Node) run(ctx context.Context, gen int, hostname, authKey string) {
	if err := os.MkdirAll(n.dir, 0o700); err != nil {
		n.set(gen, Error, "", "No se pudo crear la carpeta del equipo de Tailscale: "+err.Error())
		return
	}
	srv := &tsnet.Server{
		Dir:      n.dir,
		Hostname: hostname,
		AuthKey:  authKey,
		Logf:     func(f string, a ...any) { n.log.Debugf("tsnet: "+f, a...) },
		UserLogf: func(f string, a ...any) { n.log.Debugf("tsnet: "+f, a...) },
	}
	defer srv.Close()
	if err := srv.Start(); err != nil {
		n.set(gen, Error, "", "No se pudo arrancar Tailscale: "+err.Error())
		return
	}
	lc, err := srv.LocalClient()
	if err != nil {
		n.set(gen, Error, "", "No se pudo arrancar Tailscale: "+err.Error())
		return
	}

	// Wait until the node is signed in.
	began := time.Now()
	var dns string
	for {
		st, err := lc.StatusWithoutPeers(ctx)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			switch st.BackendState {
			case "Running":
				if st.Self != nil {
					dns = strings.TrimSuffix(st.Self.DNSName, ".")
				}
				if dns != "" {
					_ = os.WriteFile(filepath.Join(n.dir, joinedFile), []byte(dns+"\n"), 0o600)
				}
			case "NeedsLogin":
				if authKey == "" || time.Since(began) > 2*time.Minute {
					msg := "Tailscale cerró la sesión de este equipo. Crea una clave nueva y pégala aquí."
					if authKey != "" {
						msg = "No se pudo entrar a Tailscale con esa clave en 2 minutos: puede estar vencida, ya usada o mal copiada, o el servidor no llega a Tailscale. Crea una nueva y pégala aquí."
					}
					n.set(gen, NeedsKey, "", msg)
					return
				}
			case "NeedsMachineAuth":
				n.set(gen, NeedsApproval, "", "")
			}
		}
		if dns != "" {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(time.Second):
		}
	}

	// Publish on Funnel :443, retrying while the tailnet policy or HTTPS
	// setting is not ready yet.
	for {
		ln, err := srv.ListenFunnel("tcp", ":443", tsnet.FunnelOnly())
		if err == nil {
			hs := &http.Server{Handler: n.handler, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 5 * time.Minute}
			if !n.set(gen, Running, dns, "") {
				_ = ln.Close()
				return
			}
			n.log.Infof("Cloud access published on https://%s (own Tailscale machine, Funnel :443)", dns)
			if n.onRunning != nil {
				go n.onRunning(dns)
			}
			go func() {
				<-ctx.Done()
				sc, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				_ = hs.Shutdown(sc)
				cancel()
			}()
			if err := hs.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
				n.log.Warnf("cloud node stopped serving: %v", err)
				n.set(gen, Error, dns, "Se cortó la publicación: "+err.Error())
			}
			return
		}
		n.set(gen, Error, dns, funnelError(err))
		n.log.Warnf("cannot publish on Funnel :443: %v", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(30 * time.Second):
		}
	}
}

// funnelError turns Tailscale's message into a plain one for the panel.
func funnelError(err error) string {
	m := err.Error()
	l := strings.ToLower(m)
	switch {
	case strings.Contains(l, "https must be enabled") || strings.Contains(l, "https is not enabled"):
		return "Tailscale no tiene HTTPS activado. En la página de administración de Tailscale: DNS > HTTPS Certificates > Enable. Lo reintento cada 30 segundos."
	case strings.Contains(l, "funnel"):
		return "La política de Tailscale no permite Funnel a este equipo. Revisa el paso de la guía sobre Access controls (nodeAttrs \"funnel\"). Lo reintento cada 30 segundos. Detalle: " + m
	}
	return "No se pudo publicar en Funnel: " + m + ". Lo reintento cada 30 segundos."
}
