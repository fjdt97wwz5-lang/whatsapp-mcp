package wa

import (
	"bytes"
	"encoding/base64"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/mdp/qrterminal/v3"
)

// A code shaped like whatsmeow's: ref, noise key, identity key, adv secret.
const sampleCode = "2@Wq3eZ8yK1pVbN0aXcD4fG7hJ2kL5mN8pQ1rS4tU7vW0xY3zA6bC9dE2fG5hJ8kL1mN4pQ7rS0tU3vW6xY9zA2bC5dE8fG1hJ4kL7mN0p," +
	"Q2c3RlbS1ub2lzZS1rZXktZm9yLXRlc3Rpbmctb25seS0xMjM0NTY3OA==," +
	"aWRlbnRpdHkta2V5LWZvci10ZXN0aW5nLW9ubHktMDAwMDAwMDAwMDA=," +
	"YWR2LXNlY3JldC1mb3ItdGVzdGluZy1vbmx5LTk5OTk5OTk5OTk5OTk="

func TestQRPage(t *testing.T) {
	dir := t.TempDir()
	p := &qrPage{path: filepath.Join(dir, "vincular.html"), opened: true} // opened: don't launch a browser
	if err := p.showCode(sampleCode); err != nil {
		t.Fatal(err)
	}
	page, _ := os.ReadFile(p.path)
	m := regexp.MustCompile(`data:image/png;base64,([A-Za-z0-9+/=]+)`).FindSubmatch(page)
	if m == nil || !bytes.Contains(page, []byte(`http-equiv="refresh"`)) {
		t.Fatal("page lacks the QR image or the refresh")
	}
	raw, _ := base64.StdEncoding.DecodeString(string(m[1]))
	img, err := png.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if w := img.Bounds().Dx(); w < 300 || w > 900 {
		t.Errorf("unexpected QR size %d px", w)
	}
	p.finish(true, "ok")
	page, _ = os.ReadFile(p.path)
	if bytes.Contains(page, []byte("data:image")) || bytes.Contains(page, []byte("refresh")) {
		t.Error("finished page must not keep the code or keep refreshing")
	}
	p.finish(false, "late") // must not overwrite the success page
	if page2, _ := os.ReadFile(p.path); !bytes.Equal(page, page2) {
		t.Error("finish ran twice")
	}

	var term bytes.Buffer
	qrterminal.GenerateWithConfig(sampleCode, qrterminal.Config{Level: qrterminal.L, Writer: &term, HalfBlocks: true,
		BlackChar: qrterminal.BLACK_BLACK, WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
		WhiteBlackChar: qrterminal.WHITE_BLACK, QuietZone: 1})
	lines := strings.Split(strings.TrimRight(term.String(), "\n"), "\n")
	t.Logf("console QR: %d columns x %d rows", len([]rune(lines[0])), len(lines))
	if d := os.Getenv("QR_DUMP"); d != "" {
		os.WriteFile(filepath.Join(d, "page.png"), raw, 0o644)
		os.WriteFile(filepath.Join(d, "console.txt"), term.Bytes(), 0o644)
	}
}
