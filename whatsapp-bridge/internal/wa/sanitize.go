package wa

import (
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

var reservedWindowsNames = map[string]bool{
	"CON": true, "PRN": true, "AUX": true, "NUL": true,
	"COM1": true, "COM2": true, "COM3": true, "COM4": true, "COM5": true, "COM6": true, "COM7": true, "COM8": true, "COM9": true,
	"LPT1": true, "LPT2": true, "LPT3": true, "LPT4": true, "LPT5": true, "LPT6": true, "LPT7": true, "LPT8": true, "LPT9": true,
}

// SafeFilename turns a name chosen by a remote sender into something that
// can only ever be a single plain file name: no folders, no "..", no
// drive letters, no characters Windows forbids, no reserved device names,
// limited length. Returns fallback if nothing usable is left.
func SafeFilename(name, fallback string) string {
	// Keep only the last path element, whatever separator the sender used.
	name = strings.ReplaceAll(name, "\\", "/")
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r < 32 || r == 127:
			continue
		case strings.ContainsRune(`<>:"/\|?*`, r):
			b.WriteRune('_')
		case unicode.IsControl(r) || r == '\u202e' || r == '\u202d' || r == '\u200e' || r == '\u200f':
			// bidi overrides can disguise extensions (e.g. "fdp.exe" shown as "exe.pdf")
			continue
		default:
			b.WriteRune(r)
		}
	}
	name = strings.TrimRight(strings.TrimSpace(b.String()), ". ")
	name = strings.TrimLeft(name, ". ")
	if name == "" {
		name = fallback
	}
	stem := name
	if i := strings.Index(stem, "."); i >= 0 {
		stem = stem[:i]
	}
	if reservedWindowsNames[strings.ToUpper(strings.TrimSpace(stem))] {
		name = "_" + name
	}
	// Limit to 120 bytes but keep the extension.
	const max = 120
	if len(name) > max {
		ext := filepath.Ext(name)
		if len(ext) > 16 {
			ext = ""
		}
		base := name[:len(name)-len(ext)]
		cut := max - len(ext)
		for cut > 0 && !utf8.RuneStart(base[cut]) {
			cut--
		}
		name = base[:cut] + ext
	}
	return name
}

// SafeDirName makes a chat JID usable as a folder name.
func SafeDirName(jid string) string {
	return SafeFilename(strings.NewReplacer("@", "_at_", ":", "_").Replace(jid), "chat")
}
