package wa

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestSafeFilename(t *testing.T) {
	cases := map[string]string{
		"../../evil.exe":               "evil.exe",
		`..\..\Windows\System32\x.dll`: "x.dll",
		"C:\\Users\\a\\b.txt":          "b.txt",
		"factura.pdf":                  "factura.pdf",
		"con.txt":                      "_con.txt",
		"NUL":                          "_NUL",
		"a<b>c:d\"e|f?g*.txt":          "a_b_c_d_e_f_g_.txt",
		"...":                          "doc",
		"":                             "doc",
		"name.   ":                     "name",
		"inv\u202eoice.pdf":            "invoice.pdf",
		"tab\tnew\nline.txt":           "tabnewline.txt",
		".htaccess":                    "htaccess",
	}
	for in, want := range cases {
		if got := SafeFilename(in, "doc"); got != want {
			t.Errorf("SafeFilename(%q) = %q, want %q", in, got, want)
		}
	}
	long := strings.Repeat("ñ", 200) + ".pdf"
	got := SafeFilename(long, "doc")
	if len(got) > 120 || !strings.HasSuffix(got, ".pdf") {
		t.Errorf("long name not trimmed well: %d %q", len(got), got[len(got)-8:])
	}
	for _, in := range []string{"../x", "a/../../b", `..\..\c`} {
		p := filepath.Join("/base", SafeFilename(in, "d"))
		if filepath.Dir(p) != "/base" {
			t.Errorf("escaped base: %q -> %q", in, p)
		}
	}
}
