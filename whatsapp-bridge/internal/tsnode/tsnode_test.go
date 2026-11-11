package tsnode

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	waLog "go.mau.fi/whatsmeow/util/log"
)

func TestStartWithoutKeyNeedsKey(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tsnet")
	n := New(dir, http.NotFoundHandler(), waLog.Noop, nil)
	if n.Info().State != Off {
		t.Fatal("not off at start")
	}
	n.Start("whatsapp-mcp", "")
	if got := n.Info(); got.State != NeedsKey || got.HasState {
		t.Fatalf("got %+v", got)
	}
	n.Stop()
	if n.Info().State != Off {
		t.Fatal("not off after stop")
	}
}

func TestForgetWithoutStateRemovesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tsnet")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	n := New(dir, http.NotFoundHandler(), waLog.Noop, nil)
	if err := n.Forget("whatsapp-mcp"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("dir still there")
	}
}

func TestFunnelError(t *testing.T) {
	if m := funnelError(errors.New("serve config: HTTPS must be enabled to use Funnel")); !strings.Contains(m, "HTTPS") {
		t.Fatal(m)
	}
	if m := funnelError(errors.New("Funnel not available; \"funnel\" node attribute not set")); !strings.Contains(m, "nodeAttrs") {
		t.Fatal(m)
	}
}
