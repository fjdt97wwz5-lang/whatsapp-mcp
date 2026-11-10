package wa

import (
	"encoding/base64"
	"fmt"
	"html"
	"os"
	"path/filepath"

	"rsc.io/qr"
)

// qrPage is a small local web page that shows the pairing QR in the
// browser, where it always fits on screen. The console QR depends on the
// window and font size and can be cut off. The page reloads itself every
// two seconds, so it follows WhatsApp's new code (about every 20 s).
type qrPage struct {
	path   string
	opened bool
	done   bool
}

func newQRPage() *qrPage {
	// Per-user temp folder: only this user can read it, and it is deleted
	// (replaced by a page without a code) as soon as pairing ends.
	return &qrPage{path: filepath.Join(os.TempDir(), "whatsapp-mcp-vincular.html")}
}

const qrPageStyle = `body{font-family:Segoe UI,Arial,sans-serif;background:#f4f5f7;color:#111;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
main{background:#fff;border-radius:16px;box-shadow:0 2px 12px rgba(0,0,0,.08);padding:28px 32px;text-align:center;max-width:560px}
h1{font-size:22px;margin:0 0 6px}p{margin:6px 0;color:#444;line-height:1.4}
img{width:min(78vmin,440px);height:auto;image-rendering:pixelated;margin:14px 0}`

func (p *qrPage) write(title, body string, refresh bool) error {
	meta := ""
	if refresh {
		meta = `<meta http-equiv="refresh" content="2">`
	}
	doc := fmt.Sprintf(`<!doctype html><html lang="es"><head><meta charset="utf-8">%s
<title>%s</title><style>%s</style></head><body><main>%s</main></body></html>`, meta, html.EscapeString(title), qrPageStyle, body)
	tmp := p.path + ".tmp"
	if err := os.WriteFile(tmp, []byte(doc), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p.path)
}

// showCode draws the QR as a PNG inside the page and opens the page the
// first time.
func (p *qrPage) showCode(code string) error {
	c, err := qr.Encode(code, qr.L)
	if err != nil {
		return err
	}
	c.Scale = 8
	img := base64.StdEncoding.EncodeToString(c.PNG())
	body := `<h1>Vincular WhatsApp</h1>
<p>En el teléfono: <b>WhatsApp &gt; Dispositivos vinculados &gt; Vincular un dispositivo</b> y escanea este código.</p>
<img alt="Código QR para vincular WhatsApp" src="data:image/png;base64,` + img + `">
<p>El código cambia cada 20 segundos; esta página se actualiza sola.</p>`
	if err := p.write("Vincular WhatsApp", body, true); err != nil {
		return err
	}
	if !p.opened {
		p.opened = true
		return openInBrowser(p.path)
	}
	return nil
}

// finish replaces the page with one that holds no code.
func (p *qrPage) finish(ok bool, msg string) {
	if !p.opened || p.done {
		return
	}
	p.done = true
	title := "No se vinculó"
	if ok {
		title = "WhatsApp vinculado"
	}
	_ = p.write(title, "<h1>"+html.EscapeString(title)+"</h1><p>"+html.EscapeString(msg)+"</p>", false)
}

// OpenInBrowser opens a file or web address in the user's browser (on
// Windows through Explorer, so it opens as the signed-in user even from an
// elevated window).
func OpenInBrowser(target string) error { return openInBrowser(target) }
