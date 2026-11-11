package mediaread

import (
	"archive/zip"
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/xuri/excelize/v2"
)

func TestCSVSemicolonAndLatin1(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "precios.csv")
	// "Código;Producto;Precio" in Latin-1
	data := []byte("C\xf3digo;Producto;Precio\nA1;Tornillo;1,50\nA2;Tuerca;0,80\n")
	os.WriteFile(p, data, 0o600)
	r, err := Read(p, "text/csv", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "table" || !strings.Contains(r.Text, "| Código | Producto | Precio |") || !strings.Contains(r.Text, "| A2 | Tuerca | 0,80 |") {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestCSVRowLimit(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.csv")
	var b strings.Builder
	for i := 0; i < 50; i++ {
		fmt.Fprintf(&b, "%d,x\n", i)
	}
	os.WriteFile(p, []byte(b.String()), 0o600)
	r, err := Read(p, "", Limits{MaxRows: 10})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Truncated || strings.Count(r.Text, "\n") != 10 || !strings.Contains(r.Note, "10 of 50") {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestXLSX(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "lista.xlsx")
	f := excelize.NewFile()
	f.SetSheetName("Sheet1", "Precios")
	f.SetSheetRow("Precios", "A1", &[]any{"Producto", "Precio"})
	f.SetSheetRow("Precios", "A2", &[]any{"Bomba 1HP", 120.5})
	f.NewSheet("Vacía")
	if err := f.SaveAs(p); err != nil {
		t.Fatal(err)
	}
	r, err := Read(p, "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(r.Text, "## Sheet: Precios") || !strings.Contains(r.Text, "| Bomba 1HP | 120.5 |") || !strings.Contains(r.Text, "(empty)") {
		t.Fatalf("unexpected: %q", r.Text)
	}
}

func TestDOCX(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "doc.docx")
	var buf bytes.Buffer
	z := zip.NewWriter(&buf)
	w, _ := z.Create("word/document.xml")
	w.Write([]byte(`<?xml version="1.0"?><w:document xmlns:w="x"><w:body>
<w:p><w:r><w:t>Hola</w:t></w:r><w:r><w:t xml:space="preserve"> mundo</w:t></w:r></w:p>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Item</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Precio</w:t></w:r></w:p></w:tc></w:tr>
<w:tr><w:tc><w:p><w:r><w:t>Válvula</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>$30</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
<w:p><w:r><w:t>Fin</w:t></w:r></w:p></w:body></w:document>`))
	z.Close()
	os.WriteFile(p, buf.Bytes(), 0o600)
	r, err := Read(p, "", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"Hola mundo\n", "| Item | Precio |", "| Válvula | $30 |", "Fin"} {
		if !strings.Contains(r.Text, want) {
			t.Fatalf("missing %q in %q", want, r.Text)
		}
	}
}

func TestImageDownscale(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "foto.png")
	img := image.NewNRGBA(image.Rect(0, 0, 3000, 1500))
	for x := 0; x < 3000; x++ {
		img.Set(x, x%1500, color.NRGBA{255, 0, 0, 255})
	}
	f, _ := os.Create(p)
	png.Encode(f, img)
	f.Close()
	r, err := Read(p, "image/png", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Kind != "image" || r.ImageMIME != "image/jpeg" || r.Width != 1568 || r.Height != 784 || len(r.Image) == 0 {
		t.Fatalf("unexpected: %+v", r)
	}
}

// tinyPDF builds a small valid PDF with one line of text.
func tinyPDF() []byte {
	objs := []string{
		"<</Type/Catalog/Pages 2 0 R>>",
		"<</Type/Pages/Kids[3 0 R]/Count 1>>",
		"<</Type/Page/Parent 2 0 R/MediaBox[0 0 300 200]/Contents 4 0 R/Resources<</Font<</F1 5 0 R>>>>>>",
		"",
		"<</Type/Font/Subtype/Type1/BaseFont/Helvetica>>",
	}
	content := "BT /F1 12 Tf 20 100 Td (Lista de precios) Tj ET"
	objs[3] = fmt.Sprintf("<</Length %d>>\nstream\n%s\nendstream", len(content), content)
	var b bytes.Buffer
	b.WriteString("%PDF-1.4\n")
	offs := make([]int, len(objs))
	for i, o := range objs {
		offs[i] = b.Len()
		fmt.Fprintf(&b, "%d 0 obj\n%s\nendobj\n", i+1, o)
	}
	x := b.Len()
	fmt.Fprintf(&b, "xref\n0 %d\n0000000000 65535 f \n", len(objs)+1)
	for _, o := range offs {
		fmt.Fprintf(&b, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&b, "trailer\n<</Size %d/Root 1 0 R>>\nstartxref\n%d\n%%%%EOF\n", len(objs)+1, x)
	return b.Bytes()
}

func TestPDF(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.pdf")
	os.WriteFile(p, tinyPDF(), 0o600)
	r, err := Read(p, "application/pdf", Limits{})
	if err != nil {
		t.Fatal(err)
	}
	if r.Format != "pdf" || !strings.Contains(r.Text, "Lista de precios") {
		t.Fatalf("unexpected: %+v", r)
	}
}

func TestDetect(t *testing.T) {
	cases := map[string]string{"pdf": "%PDF-1.7", "audio": "OggS\x00", "old-office": "\xD0\xCF\x11\xE0xx"}
	for want, head := range cases {
		if got := Detect("x.bin", "", []byte(head)); got != want {
			t.Errorf("Detect(%q) = %s, want %s", head, got, want)
		}
	}
	if got := Detect("price.xlsx", "", []byte("PK\x03\x04")); got != "xlsx" {
		t.Errorf("xlsx: %s", got)
	}
}
