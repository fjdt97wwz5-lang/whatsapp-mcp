package transcribe

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A stand-in for faster-whisper: the GPU "fails" (like a missing cuDNN) and
// the CPU works, so the fallback path is exercised.
const stubFW = `
class _Seg:
    def __init__(self, t): self.text = t
class _Info:
    language = "es"
    duration = 3.2
class WhisperModel:
    def __init__(self, name, device="auto", compute_type="default", download_root=None, local_files_only=False):
        if device == "cuda":
            raise RuntimeError("Could not load library cudnn_ops64_9.dll")
        self.device = device
    def transcribe(self, audio, language=None, beam_size=5, vad_filter=False):
        return iter([_Seg(" Hola, te mando "), _Seg("la lista de precios. ")]), _Info()
`

const stubUtils = `
import os
def download_model(name, cache_dir=None):
    p = os.path.join(cache_dir, "m")
    os.makedirs(p, exist_ok=True)
    open(os.path.join(p, "model.bin"), "w").write("x")
    return p
`

const stubNumpy = `
def zeros(n, dtype=None): return [0.0] * n
`

func stubPython(t *testing.T) string {
	t.Helper()
	py, err := exec.LookPath("python3")
	if err != nil {
		t.Skip("python3 not available")
	}
	venv := filepath.Join(t.TempDir(), "venv")
	if out, err := exec.Command(py, "-m", "venv", "--without-pip", venv).CombinedOutput(); err != nil {
		t.Skipf("venv not available: %v %s", err, out)
	}
	sp, _ := filepath.Glob(filepath.Join(venv, "lib", "python3*", "site-packages"))
	if len(sp) == 0 {
		t.Skip("no site-packages")
	}
	fw := filepath.Join(sp[0], "faster_whisper")
	os.MkdirAll(fw, 0o700)
	os.WriteFile(filepath.Join(fw, "__init__.py"), []byte(stubFW), 0o600)
	os.WriteFile(filepath.Join(fw, "utils.py"), []byte(stubUtils), 0o600)
	os.WriteFile(filepath.Join(sp[0], "numpy.py"), []byte(stubNumpy), 0o600)
	return filepath.Join(venv, "bin", "python")
}

func TestInstallCheckAndTranscribeWithCPUFallback(t *testing.T) {
	m := &Manager{Dir: filepath.Join(t.TempDir(), "transcribe"), PythonExe: stubPython(t)}
	if m.Installed() {
		t.Fatal("installed too early")
	}
	if _, err := m.Transcribe(context.Background(), "x.ogg", "auto"); err != ErrNotInstalled {
		t.Fatalf("want ErrNotInstalled, got %v", err)
	}
	if err := m.Install(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for m.Status().Installing && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	st := m.Status()
	if !st.Installed || st.Percent != 100 || st.Device != "cpu" || !strings.Contains(st.Detail, "cudnn") {
		t.Fatalf("unexpected state: %+v", st)
	}
	audio := filepath.Join(t.TempDir(), "nota.ogg")
	os.WriteFile(audio, []byte("OggS"), 0o600)
	r, err := m.Transcribe(context.Background(), audio, "es")
	if err != nil {
		t.Fatal(err)
	}
	if r.Text != "Hola, te mando la lista de precios." || r.Device != "cpu" || r.Language != "es" || r.Duration != 3.2 {
		t.Fatalf("unexpected result: %+v", r)
	}
}

func TestDownloadRejectsWrongHash(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("tampered")) }))
	defer srv.Close()
	m := &Manager{Dir: t.TempDir()}
	dst := filepath.Join(m.Dir, "p.nupkg")
	err := m.download(context.Background(), srv.URL, strings.Repeat("0", 64), dst, 0, 10)
	if err == nil || !strings.Contains(err.Error(), "no es el esperado") {
		t.Fatalf("want hash error, got %v", err)
	}
	if _, err := os.Stat(dst); err == nil {
		t.Fatal("a bad download must be deleted")
	}
	sum := sha256.Sum256([]byte("tampered"))
	if err := m.download(context.Background(), srv.URL, hex.EncodeToString(sum[:]), dst, 0, 10); err != nil {
		t.Fatalf("good hash refused: %v", err)
	}
}

func TestExtractToolsRefusesEscape(t *testing.T) {
	dir := t.TempDir()
	mk := func(names ...string) string {
		p := filepath.Join(dir, "x.zip")
		f, _ := os.Create(p)
		z := zip.NewWriter(f)
		for _, n := range names {
			w, _ := z.Create(n)
			w.Write([]byte("x"))
		}
		z.Close()
		f.Close()
		return p
	}
	dst := filepath.Join(dir, "py")
	if err := extractTools(mk("tools/python.exe", "tools/Lib/os.py", "other/skip.txt"), dst); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dst, "Lib", "os.py")); err != nil {
		t.Fatal("missing extracted file")
	}
	if _, err := os.Stat(filepath.Join(dst, "skip.txt")); err == nil {
		t.Fatal("extracted a file outside tools/")
	}
	if err := extractTools(mk("tools/../../evil.txt"), dst); err == nil {
		t.Fatal("path escape not refused")
	}
}

func TestPipProgress(t *testing.T) {
	p := &pipProgress{total: 4}
	pct, step := p.line("Collecting faster-whisper==1.2.1 (from -r req.txt (line 6))")
	if pct <= 10 || !strings.Contains(step, "faster-whisper") {
		t.Fatalf("got %d %q", pct, step)
	}
	if pct, _ := p.line("Installing collected packages: a, b"); pct != 68 {
		t.Fatalf("got %d", pct)
	}
	if n := pipPackages(); n < 20 {
		t.Fatalf("requirements has %d packages", n)
	}
}

func TestRequirementsArePinnedWithHashes(t *testing.T) {
	for _, l := range strings.Split(strings.TrimSpace(string(requirementsWin)), "\n") {
		if !strings.Contains(l, "==") || !strings.Contains(l, "--hash=sha256:") {
			t.Fatalf("unpinned line: %q", l)
		}
	}
}
