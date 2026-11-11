// Package mediaread turns a received WhatsApp attachment into something
// Claude can read: photos become a smaller JPEG image, spreadsheets and CSV
// become tables, PDF and Word become text. Voice notes are handled by the
// transcribe package. Everything runs locally on the server.
package mediaread

import (
	"archive/zip"
	"bytes"
	"encoding/csv"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	_ "image/gif" // register decoders
	_ "image/png"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// Limits keep a single answer to Claude reasonable.
type Limits struct {
	MaxRows      int // rows per sheet / CSV
	MaxChars     int // total text characters
	MaxImageSide int // longest side of the image sent to Claude, in pixels
}

// DefaultLimits are used when a field is zero.
var DefaultLimits = Limits{MaxRows: 300, MaxChars: 60000, MaxImageSide: 1568}

func (l Limits) fill() Limits {
	if l.MaxRows <= 0 {
		l.MaxRows = DefaultLimits.MaxRows
	}
	if l.MaxChars <= 0 {
		l.MaxChars = DefaultLimits.MaxChars
	}
	if l.MaxImageSide <= 0 {
		l.MaxImageSide = DefaultLimits.MaxImageSide
	}
	return l
}

// Result is what was read from a file.
type Result struct {
	Kind      string `json:"kind"` // image, table, text, unsupported
	Format    string `json:"format"`
	Text      string `json:"text,omitempty"`
	Image     []byte `json:"-"`
	ImageMIME string `json:"-"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Note      string `json:"note,omitempty"`
}

// Detect works out the format of a file from its name, MIME type and first bytes.
func Detect(path, mime string, head []byte) string {
	ext := strings.ToLower(filepath.Ext(path))
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	switch {
	case bytes.HasPrefix(head, []byte("%PDF")):
		return "pdf"
	case bytes.HasPrefix(head, []byte{0xFF, 0xD8, 0xFF}), bytes.HasPrefix(head, []byte("\x89PNG")),
		bytes.HasPrefix(head, []byte("GIF8")), len(head) > 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "image"
	case len(head) > 12 && string(head[4:8]) == "ftyp" && (strings.Contains(string(head[8:12]), "hei") || strings.Contains(string(head[8:12]), "mif")):
		return "heic"
	case bytes.HasPrefix(head, []byte("OggS")):
		return "audio"
	case bytes.HasPrefix(head, []byte{0xD0, 0xCF, 0x11, 0xE0}):
		return "old-office" // .xls / .doc (binary Office 97-2003)
	case bytes.HasPrefix(head, []byte("PK\x03\x04")):
		switch ext {
		case ".xlsx", ".xlsm":
			return "xlsx"
		case ".docx":
			return "docx"
		}
		switch mime {
		case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
			return "xlsx"
		case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
			return "docx"
		}
		return "zip"
	}
	switch ext {
	case ".csv":
		return "csv"
	case ".tsv":
		return "tsv"
	case ".txt", ".md", ".json", ".xml", ".html", ".htm", ".log", ".vcf":
		return "text"
	case ".mp3", ".m4a", ".ogg", ".opus", ".wav", ".aac", ".amr":
		return "audio"
	case ".mp4", ".mov", ".3gp", ".mkv":
		return "video"
	}
	switch {
	case mime == "text/csv":
		return "csv"
	case strings.HasPrefix(mime, "text/"):
		return "text"
	case strings.HasPrefix(mime, "audio/"):
		return "audio"
	case strings.HasPrefix(mime, "video/"):
		return "video"
	case strings.HasPrefix(mime, "image/"):
		return "image"
	}
	if utf8.Valid(head) && !bytes.ContainsRune(head, 0) && len(head) > 0 {
		return "text"
	}
	return "unknown"
}

// Read opens a file and converts it. Audio and video are not handled here.
func Read(path, mime string, lim Limits) (*Result, error) {
	lim = lim.fill()
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	head := make([]byte, 512)
	n, _ := io.ReadFull(f, head)
	f.Close()
	format := Detect(path, mime, head[:n])
	switch format {
	case "image":
		return readImage(path, lim)
	case "heic":
		return &Result{Kind: "unsupported", Format: format, Note: "Photo in HEIC format (iPhone) cannot be converted on the server. Ask the sender to send it as a normal photo."}, nil
	case "pdf":
		return readPDF(path, lim)
	case "xlsx":
		return readXLSX(path, lim)
	case "docx":
		return readDOCX(path, lim)
	case "csv", "tsv":
		return readCSV(path, format == "tsv", lim)
	case "text":
		return readText(path, lim)
	case "old-office":
		return &Result{Kind: "unsupported", Format: format, Note: "Old Excel/Word file (.xls/.doc, Office 97-2003). Only .xlsx, .docx, .csv and .pdf can be read; ask the sender for a newer format."}, nil
	case "audio", "video":
		return &Result{Kind: "unsupported", Format: format}, nil
	}
	return &Result{Kind: "unsupported", Format: format, Note: "This kind of file cannot be read as text."}, nil
}

func readImage(path string, lim Limits) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return nil, fmt.Errorf("the photo could not be opened: %w", err)
	}
	if cfg.Width*cfg.Height > 80_000_000 {
		return nil, errors.New("the photo is too large to open")
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	src, _, err := image.Decode(f)
	if err != nil {
		return nil, fmt.Errorf("the photo could not be opened: %w", err)
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	nw, nh := w, h
	if m := max(w, h); m > lim.MaxImageSide {
		nw = w * lim.MaxImageSide / m
		nh = h * lim.MaxImageSide / m
	}
	dst := image.NewRGBA(image.Rect(0, 0, max(nw, 1), max(nh, 1)))
	// White background so transparent stickers/PNGs read well as JPEG.
	draw.Draw(dst, dst.Bounds(), image.White, image.Point{}, draw.Src)
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Over, nil)
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return nil, err
	}
	return &Result{Kind: "image", Format: "image", Image: buf.Bytes(), ImageMIME: "image/jpeg", Width: nw, Height: nh}, nil
}

// textBuilder collects text up to a character limit.
type textBuilder struct {
	b         strings.Builder
	max       int
	truncated bool
}

func (t *textBuilder) write(s string) bool {
	if t.truncated {
		return false
	}
	if t.b.Len()+len(s) > t.max {
		rest := t.max - t.b.Len()
		if rest > 0 {
			cut := s[:rest]
			for !utf8.ValidString(cut) && len(cut) > 0 {
				cut = cut[:len(cut)-1]
			}
			t.b.WriteString(cut)
		}
		t.truncated = true
		return false
	}
	t.b.WriteString(s)
	return true
}

func cell(s string) string {
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "/")
	return strings.TrimSpace(s)
}

// writeTable writes rows as a pipe-separated table (first row as header).
func writeTable(t *textBuilder, rows [][]string, maxRows int) (shown, total int) {
	total = len(rows)
	// Drop fully empty trailing columns/rows.
	width := 0
	for _, r := range rows {
		for i := len(r) - 1; i >= 0; i-- {
			if strings.TrimSpace(r[i]) != "" {
				width = max(width, i+1)
				break
			}
		}
	}
	for i, r := range rows {
		if i >= maxRows {
			break
		}
		empty := true
		cells := make([]string, width)
		for j := 0; j < width; j++ {
			if j < len(r) {
				cells[j] = cell(r[j])
				if cells[j] != "" {
					empty = false
				}
			}
		}
		if empty {
			continue
		}
		if !t.write("| " + strings.Join(cells, " | ") + " |\n") {
			return shown, total
		}
		shown = i + 1
	}
	return shown, total
}

func readCSV(path string, tab bool, lim Limits) (*Result, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	data = bytes.TrimPrefix(data, []byte("\xEF\xBB\xBF"))
	if !utf8.Valid(data) {
		data = latin1ToUTF8(data)
	}
	r := csv.NewReader(bytes.NewReader(data))
	r.FieldsPerRecord = -1
	r.LazyQuotes = true
	switch {
	case tab:
		r.Comma = '\t'
	default:
		// Spanish Excel exports often use ";".
		first := string(data[:min(len(data), 2000)])
		if strings.Count(first, ";") > strings.Count(first, ",") {
			r.Comma = ';'
		}
	}
	rows, err := r.ReadAll()
	if err != nil && len(rows) == 0 {
		return nil, fmt.Errorf("the CSV could not be read: %w", err)
	}
	t := &textBuilder{max: lim.MaxChars}
	shown, total := writeTable(t, rows, lim.MaxRows)
	res := &Result{Kind: "table", Format: "csv", Text: t.b.String(), Truncated: t.truncated || shown < total}
	if res.Truncated {
		res.Note = fmt.Sprintf("Showing %d of %d rows.", shown, total)
	}
	return res, nil
}

func latin1ToUTF8(b []byte) []byte {
	var out bytes.Buffer
	for _, c := range b {
		out.WriteRune(rune(c))
	}
	return out.Bytes()
}

func readXLSX(path string, lim Limits) (*Result, error) {
	f, err := excelize.OpenFile(path, excelize.Options{})
	if err != nil {
		return nil, fmt.Errorf("the Excel file could not be opened: %w", err)
	}
	defer f.Close()
	t := &textBuilder{max: lim.MaxChars}
	var notes []string
	cut := false
	for _, sheet := range f.GetSheetList() {
		rows, err := f.GetRows(sheet)
		if err != nil {
			notes = append(notes, fmt.Sprintf("Sheet %q could not be read.", sheet))
			continue
		}
		if !t.write(fmt.Sprintf("## Sheet: %s\n", sheet)) {
			break
		}
		if len(rows) == 0 {
			t.write("(empty)\n\n")
			continue
		}
		shown, total := writeTable(t, rows, lim.MaxRows)
		if shown < total {
			cut = true
			notes = append(notes, fmt.Sprintf("Sheet %q: showing %d of %d rows.", sheet, shown, total))
		}
		if !t.write("\n") {
			break
		}
	}
	res := &Result{Kind: "table", Format: "xlsx", Text: t.b.String(), Truncated: t.truncated || cut}
	if t.truncated {
		notes = append(notes, "The text was cut at the size limit.")
	}
	res.Note = strings.Join(notes, " ")
	return res, nil
}

func readPDF(path string, lim Limits) (res *Result, err error) {
	defer func() {
		if r := recover(); r != nil { // the PDF library panics on some broken files
			res, err = nil, fmt.Errorf("the PDF could not be read")
		}
	}()
	f, r, err := pdf.Open(path)
	if err != nil {
		return nil, fmt.Errorf("the PDF could not be opened: %w", err)
	}
	defer f.Close()
	t := &textBuilder{max: lim.MaxChars}
	pages := r.NumPage()
	for i := 1; i <= pages; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		rows, err := p.GetTextByRow()
		if err != nil {
			continue
		}
		var lines []string
		for _, row := range rows {
			var parts []string
			for _, w := range row.Content {
				parts = append(parts, w.S)
			}
			if l := strings.TrimSpace(strings.Join(parts, " ")); l != "" {
				lines = append(lines, l)
			}
		}
		if len(lines) == 0 {
			continue
		}
		if !t.write(fmt.Sprintf("## Page %d\n%s\n\n", i, strings.Join(lines, "\n"))) {
			break
		}
	}
	res = &Result{Kind: "text", Format: "pdf", Text: t.b.String(), Truncated: t.truncated}
	if strings.TrimSpace(res.Text) == "" {
		res.Note = "This PDF has no text inside (it looks like a scan or a photo). It cannot be read as text."
	} else if t.truncated {
		res.Note = "The text was cut at the size limit."
	}
	return res, nil
}

func readDOCX(path string, lim Limits) (*Result, error) {
	z, err := zip.OpenReader(path)
	if err != nil {
		return nil, fmt.Errorf("the Word file could not be opened: %w", err)
	}
	defer z.Close()
	var doc *zip.File
	for _, f := range z.File {
		if f.Name == "word/document.xml" {
			doc = f
		}
	}
	if doc == nil {
		return nil, errors.New("this is not a Word document")
	}
	if doc.UncompressedSize64 > 200<<20 {
		return nil, errors.New("the Word document is too large")
	}
	rc, err := doc.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	text, err := docxText(io.LimitReader(rc, 200<<20))
	if err != nil {
		return nil, fmt.Errorf("the Word file could not be read: %w", err)
	}
	t := &textBuilder{max: lim.MaxChars}
	t.write(text)
	res := &Result{Kind: "text", Format: "docx", Text: t.b.String(), Truncated: t.truncated}
	if t.truncated {
		res.Note = "The text was cut at the size limit."
	}
	return res, nil
}

// docxText walks document.xml: paragraphs become lines, table rows become
// "| a | b |" lines.
func docxText(r io.Reader) (string, error) {
	dec := xml.NewDecoder(r)
	var out strings.Builder
	var para strings.Builder
	var rowCells []string
	inTable := 0
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return out.String(), err
		}
		switch e := tok.(type) {
		case xml.StartElement:
			switch e.Name.Local {
			case "t":
				inText = true
			case "tab":
				para.WriteString("\t")
			case "br", "cr":
				para.WriteString("\n")
			case "tbl":
				inTable++
			case "tr":
				rowCells = nil
			}
		case xml.EndElement:
			switch e.Name.Local {
			case "t":
				inText = false
			case "p":
				if inTable > 0 {
					para.WriteString(" ")
				} else {
					out.WriteString(strings.TrimRight(para.String(), " ") + "\n")
					para.Reset()
				}
			case "tc":
				rowCells = append(rowCells, cell(para.String()))
				para.Reset()
			case "tr":
				out.WriteString("| " + strings.Join(rowCells, " | ") + " |\n")
			case "tbl":
				inTable--
				out.WriteString("\n")
			}
		case xml.CharData:
			if inText {
				para.Write(e)
			}
		}
	}
	return strings.TrimSpace(out.String()) + "\n", nil
}

func readText(path string, lim Limits) (*Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(lim.MaxChars)*4))
	if err != nil {
		return nil, err
	}
	if !utf8.Valid(data) {
		data = latin1ToUTF8(data)
	}
	t := &textBuilder{max: lim.MaxChars}
	t.write(string(data))
	res := &Result{Kind: "text", Format: "text", Text: t.b.String(), Truncated: t.truncated}
	if t.truncated {
		res.Note = "The text was cut at the size limit."
	}
	return res, nil
}
