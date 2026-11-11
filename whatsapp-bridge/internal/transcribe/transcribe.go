// Package transcribe turns voice notes into text on the server itself, with
// faster-whisper on the NVIDIA GPU (CPU when the GPU can't be used).
//
// Nothing is bundled in the installer: the owner presses "Instalar
// transcripción" in the panel and the server downloads a private copy of
// Python from nuget.org and the libraries from pypi.org. Every file is
// checked against a SHA-256 fixed in this program, so a tampered download is
// refused. The speech model (about 1.6 GB) comes from Hugging Face. After
// that, transcription works offline and audio never leaves the server.
package transcribe

import (
	"archive/zip"
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

//go:embed worker.py
var workerPy []byte

//go:embed requirements-win.txt
var requirementsWin []byte

// Pinned private Python for Windows (nuget.org package "python").
const (
	PythonVersion = "3.12.10"
	pythonURL     = "https://api.nuget.org/v3-flatcontainer/python/3.12.10/python.3.12.10.nupkg"
	pythonSHA256  = "0eb85c2dfccccf1b17352de4c397f69194035b7d37149eacc16f1147d93de3b8"
	// Model is the faster-whisper model used.
	Model = "large-v3-turbo"
	// modelBytes is roughly the size of the model download, for progress.
	modelBytes = 1_620_000_000
)

// Logger is the small logging interface used here.
type Logger interface {
	Infof(string, ...any)
	Warnf(string, ...any)
}

// State is what the panel shows.
type State struct {
	Supported  bool   `json:"supported"`
	Installed  bool   `json:"installed"`
	Installing bool   `json:"installing"`
	Percent    int    `json:"percent"`
	Step       string `json:"step,omitempty"`
	Error      string `json:"error,omitempty"`
	Device     string `json:"device,omitempty"` // cuda or cpu, from the last check
	Model      string `json:"model"`
	Detail     string `json:"detail,omitempty"`
}

// Manager installs and runs the transcription add-on.
type Manager struct {
	Dir string // e.g. C:\ProgramData\WhatsAppMCP\transcribe
	Log Logger

	// For tests: an existing Python to use instead of installing one.
	PythonExe string
	// For tests: override the download (URL and its SHA-256).
	PythonURL, PythonSHA string
	HTTP                 *http.Client

	mu  sync.Mutex
	st  State
	run sync.Mutex // one transcription at a time (one GPU)
}

type installedInfo struct {
	Python  string    `json:"python"`
	Model   string    `json:"model"`
	Device  string    `json:"device"`
	Detail  string    `json:"detail,omitempty"`
	At      time.Time `json:"at"`
	Version int       `json:"version"`
}

const installVersion = 1

func (m *Manager) pythonDir() string  { return filepath.Join(m.Dir, "python") }
func (m *Manager) modelDir() string   { return filepath.Join(m.Dir, "models") }
func (m *Manager) workerPath() string { return filepath.Join(m.Dir, "worker", "worker.py") }
func (m *Manager) markerPath() string { return filepath.Join(m.Dir, "installed.json") }

func (m *Manager) python() string {
	if m.PythonExe != "" {
		return m.PythonExe
	}
	return filepath.Join(m.pythonDir(), "python.exe")
}

// Supported reports whether the add-on can be installed on this system.
func (m *Manager) Supported() bool { return runtime.GOOS == "windows" || m.PythonExe != "" }

func (m *Manager) readMarker() (*installedInfo, bool) {
	b, err := os.ReadFile(m.markerPath())
	if err != nil {
		return nil, false
	}
	var ii installedInfo
	if json.Unmarshal(b, &ii) != nil || ii.Version != installVersion {
		return nil, false
	}
	if _, err := os.Stat(m.python()); err != nil {
		return nil, false
	}
	return &ii, true
}

// Status returns the install state.
func (m *Manager) Status() State {
	m.mu.Lock()
	st := m.st
	m.mu.Unlock()
	st.Supported = m.Supported()
	st.Model = Model
	if !st.Installing {
		if ii, ok := m.readMarker(); ok {
			st.Installed, st.Device, st.Detail = true, ii.Device, ii.Detail
			if st.Error == "" {
				st.Percent = 100
			}
		}
	}
	return st
}

// Installed reports whether transcription is ready.
func (m *Manager) Installed() bool { _, ok := m.readMarker(); return ok }

func (m *Manager) set(pct int, step string) {
	m.mu.Lock()
	if pct > m.st.Percent || pct == 0 {
		m.st.Percent = pct
	}
	if step != "" {
		m.st.Step = step
	}
	m.mu.Unlock()
}

// Install starts installing in the background. It returns an error when an
// install is already running or the system is not supported.
func (m *Manager) Install() error {
	if !m.Supported() {
		return errors.New("la transcripción solo se instala en el server Windows")
	}
	m.mu.Lock()
	if m.st.Installing {
		m.mu.Unlock()
		return errors.New("ya se está instalando")
	}
	m.st = State{Installing: true, Step: "Empezando"}
	m.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Hour)
		defer cancel()
		err := m.install(ctx)
		m.mu.Lock()
		m.st.Installing = false
		if err != nil {
			m.st.Error = err.Error()
			m.st.Step = "No se pudo instalar"
		} else {
			m.st.Percent, m.st.Step, m.st.Error = 100, "Listo", ""
		}
		m.mu.Unlock()
		if err != nil && m.Log != nil {
			m.Log.Warnf("transcription install failed: %v", err)
		}
	}()
	return nil
}

func (m *Manager) install(ctx context.Context) error {
	if err := os.MkdirAll(m.Dir, 0o700); err != nil {
		return err
	}
	_ = os.Remove(m.markerPath())
	if m.PythonExe == "" {
		if _, err := os.Stat(m.python()); err != nil {
			m.set(1, "Descargando Python (14 MB)")
			pkg := filepath.Join(m.Dir, "python.nupkg")
			url, sum := pythonURL, pythonSHA256
			if m.PythonURL != "" {
				url, sum = m.PythonURL, m.PythonSHA
			}
			if err := m.download(ctx, url, sum, pkg, 1, 8); err != nil {
				return fmt.Errorf("descargar Python: %w", err)
			}
			m.set(8, "Descomprimiendo Python")
			if err := extractTools(pkg, m.pythonDir()); err != nil {
				return fmt.Errorf("descomprimir Python: %w", err)
			}
			os.Remove(pkg)
		}
		m.set(10, "Instalando las librerías (1,4 GB, tarda varios minutos)")
		if err := m.pipInstall(ctx); err != nil {
			return fmt.Errorf("instalar las librerías: %w", err)
		}
	}
	if err := m.writeWorker(); err != nil {
		return err
	}
	m.set(80, "Descargando el modelo de voz (1,6 GB)")
	stop := m.watchModelSize(80, 96)
	_, err := m.runWorker(ctx, false, "download")
	stop()
	if err != nil {
		return fmt.Errorf("descargar el modelo: %w", err)
	}
	m.set(97, "Probando la tarjeta gráfica")
	res, err := m.runWorker(ctx, true, "check")
	if err != nil {
		return fmt.Errorf("probar la transcripción: %w", err)
	}
	dev, _ := res["device"].(string)
	detail := ""
	if fb, ok := res["fallback"].([]any); ok && len(fb) > 0 {
		detail = fmt.Sprint(fb[0])
	}
	ii := installedInfo{Python: PythonVersion, Model: Model, Device: dev, Detail: detail, At: time.Now(), Version: installVersion}
	b, _ := json.MarshalIndent(ii, "", "  ")
	return os.WriteFile(m.markerPath(), b, 0o600)
}

// download fetches url into dst, checks its SHA-256 and reports progress
// between pctFrom and pctTo.
func (m *Manager) download(ctx context.Context, url, sum, dst string, pctFrom, pctTo int) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	cl := m.HTTP
	if cl == nil {
		cl = &http.Client{Timeout: 20 * time.Minute}
	}
	resp, err := cl.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("el servidor respondió %s", resp.Status)
	}
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	total := resp.ContentLength
	var done int64
	buf := make([]byte, 256<<10)
	for {
		n, rerr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				f.Close()
				return err
			}
			h.Write(buf[:n])
			done += int64(n)
			if total > 0 {
				m.set(pctFrom+int(int64(pctTo-pctFrom)*done/total), "")
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			f.Close()
			return rerr
		}
	}
	if err := f.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); !strings.EqualFold(got, sum) {
		os.Remove(dst)
		return fmt.Errorf("el archivo descargado no es el esperado (SHA-256 %s); no se usa", got)
	}
	return nil
}

// extractTools unpacks the "tools/" folder of the nuget package (the Python
// itself) into dst, refusing any path that would leave dst.
func extractTools(pkg, dst string) error {
	z, err := zip.OpenReader(pkg)
	if err != nil {
		return err
	}
	defer z.Close()
	tmp := dst + ".tmp"
	os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return err
	}
	base, _ := filepath.Abs(tmp)
	for _, f := range z.File {
		name := strings.ReplaceAll(f.Name, "\\", "/")
		if !strings.HasPrefix(name, "tools/") || strings.HasSuffix(name, "/") {
			continue
		}
		rel := strings.TrimPrefix(name, "tools/")
		p := filepath.Join(base, filepath.FromSlash(rel))
		if r, err := filepath.Rel(base, p); err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
			return fmt.Errorf("unsafe path in package: %s", f.Name)
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return err
		}
		w, err := os.OpenFile(p, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			rc.Close()
			return err
		}
		_, err = io.Copy(w, io.LimitReader(rc, 512<<20))
		rc.Close()
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
	}
	os.RemoveAll(dst)
	return os.Rename(tmp, dst)
}

// pipPackages is how many packages the requirements file pins.
func pipPackages() int {
	n := 0
	for _, l := range strings.Split(string(requirementsWin), "\n") {
		if strings.Contains(l, "==") {
			n++
		}
	}
	return n
}

// pipProgress maps a line of pip output to a percentage of the pip step.
type pipProgress struct {
	total, seen int
}

func (p *pipProgress) line(l string) (pct int, step string) {
	l = strings.TrimSpace(l)
	switch {
	case strings.HasPrefix(l, "Collecting "), strings.HasPrefix(l, "Processing "):
		p.seen++
		name := strings.Fields(strings.TrimPrefix(strings.TrimPrefix(l, "Collecting "), "Processing "))
		s := ""
		if len(name) > 0 {
			s = "Descargando " + strings.SplitN(name[0], "==", 2)[0]
		}
		return 10 + 55*min(p.seen, p.total)/max(p.total, 1), s
	case strings.HasPrefix(l, "Installing collected packages"):
		return 68, "Instalando las librerías"
	case strings.HasPrefix(l, "Successfully installed"):
		return 79, ""
	}
	return 0, ""
}

func (m *Manager) pipInstall(ctx context.Context) error {
	req := filepath.Join(m.Dir, "requirements.txt")
	if err := os.WriteFile(req, requirementsWin, 0o600); err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, m.python(), "-I", "-m", "pip", "install",
		"--no-input", "--disable-pip-version-check", "--no-warn-script-location", "--no-cache-dir",
		"--progress-bar", "off", "--require-hashes", "--only-binary=:all:", "-r", req)
	cmd.Env = m.env(false)
	hideWindow(cmd)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	pp := &pipProgress{total: pipPackages()}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		if pct, step := pp.line(sc.Text()); pct > 0 {
			m.set(pct, step)
		}
	}
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("%v: %s", err, lastLines(stderr.String(), 5))
	}
	return nil
}

func lastLines(s string, n int) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, " | ")
}

func (m *Manager) writeWorker() error {
	if err := os.MkdirAll(filepath.Dir(m.workerPath()), 0o700); err != nil {
		return err
	}
	if cur, err := os.ReadFile(m.workerPath()); err == nil && bytes.Equal(cur, workerPy) {
		return nil
	}
	return os.WriteFile(m.workerPath(), workerPy, 0o600)
}

// dirSize adds up the bytes under dir.
func dirSize(dir string) int64 {
	var n int64
	filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n
}

func (m *Manager) watchModelSize(from, to int) func() {
	done := make(chan struct{})
	go func() {
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-t.C:
				m.set(from+int(int64(to-from)*min(dirSize(m.modelDir()), modelBytes)/modelBytes), "")
			}
		}
	}()
	return func() { close(done) }
}

func (m *Manager) env(offline bool) []string {
	env := []string{}
	for _, kv := range os.Environ() {
		k := strings.ToUpper(strings.SplitN(kv, "=", 2)[0])
		if strings.HasPrefix(k, "PYTHON") || strings.HasPrefix(k, "PIP_") || strings.HasPrefix(k, "HF_") {
			continue // never let outside settings change what runs
		}
		env = append(env, kv)
	}
	env = append(env, "PYTHONUTF8=1", "PYTHONNOUSERSITE=1", "PYTHONDONTWRITEBYTECODE=1",
		"HF_HUB_DISABLE_TELEMETRY=1", "HF_HUB_DISABLE_PROGRESS_BARS=1", "MODEL_DIR="+m.modelDir(),
		"HF_HOME="+filepath.Join(m.Dir, "hf"), "WHISPER_MODEL="+Model)
	if offline {
		env = append(env, "HF_HUB_OFFLINE=1")
	}
	return env
}

// runWorker runs worker.py once and returns its JSON answer.
func (m *Manager) runWorker(ctx context.Context, offline bool, args ...string) (map[string]any, error) {
	if err := m.writeWorker(); err != nil {
		return nil, err
	}
	cmd := exec.CommandContext(ctx, m.python(), append([]string{"-I", m.workerPath()}, args...)...)
	cmd.Env = m.env(offline)
	cmd.Dir = m.Dir
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	runErr := cmd.Run()
	var res map[string]any
	line := strings.TrimSpace(stdout.String())
	if i := strings.LastIndex(line, "\n"); i >= 0 {
		line = line[i+1:]
	}
	if err := json.Unmarshal([]byte(line), &res); err != nil {
		if runErr != nil {
			return nil, fmt.Errorf("%v: %s", runErr, lastLines(stderr.String(), 4))
		}
		return nil, fmt.Errorf("unexpected answer from the transcriber: %s", lastLines(stderr.String(), 4))
	}
	if e, ok := res["error"].(string); ok && e != "" {
		return nil, errors.New(e)
	}
	return res, nil
}

// Result is a finished transcription.
type Result struct {
	Text     string  `json:"text"`
	Language string  `json:"language"`
	Duration float64 `json:"duration_seconds"`
	Device   string  `json:"device"`
	Model    string  `json:"model"`
	Seconds  float64 `json:"took_seconds"`
}

// ErrNotInstalled means the add-on has not been installed from the panel.
var ErrNotInstalled = errors.New("voice note transcription is not installed yet: open the panel, Ajustes, and press \"Instalar transcripción\"")

// Transcribe turns an audio file into text. lang is auto, es or en.
func (m *Manager) Transcribe(ctx context.Context, path, lang string) (*Result, error) {
	if !m.Installed() {
		return nil, ErrNotInstalled
	}
	m.run.Lock()
	defer m.run.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 15*time.Minute)
	defer cancel()
	res, err := m.runWorker(ctx, true, "transcribe", path, lang)
	if err != nil {
		return nil, fmt.Errorf("transcription failed: %w", err)
	}
	b, _ := json.Marshal(res)
	var r Result
	_ = json.Unmarshal(b, &r)
	if d, ok := res["duration"].(float64); ok {
		r.Duration = d
	}
	if s, ok := res["seconds"].(float64); ok {
		r.Seconds = s
	}
	return &r, nil
}

// Remove deletes the add-on (Python, libraries and model).
func (m *Manager) Remove() error {
	m.mu.Lock()
	if m.st.Installing {
		m.mu.Unlock()
		return errors.New("se está instalando; espera a que termine")
	}
	m.st = State{}
	m.mu.Unlock()
	m.run.Lock()
	defer m.run.Unlock()
	return os.RemoveAll(m.Dir)
}
