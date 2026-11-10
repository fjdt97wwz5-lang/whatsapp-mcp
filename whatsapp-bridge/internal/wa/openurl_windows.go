//go:build windows

package wa

import (
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// OpenURL opens a web address in the signed-in user's default browser.
//
// Handing a web address straight to explorer.exe from an elevated program
// (the installer, an administrator window) only opens File Explorer. A file
// does work, so the address goes into an Internet shortcut (.url) in a
// private temporary folder and explorer.exe opens that file as the normal
// user, like a double-click. The returned cleanup removes the shortcut; call
// it once the browser had time to read it.
func OpenURL(u string) (cleanup func(), err error) {
	dir, err := os.MkdirTemp("", "wmcp-panel-")
	if err != nil {
		return func() {}, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	f := filepath.Join(dir, "Panel de WhatsApp MCP.url")
	if err := os.WriteFile(f, []byte("[InternetShortcut]\r\nURL="+u+"\r\n"), 0o600); err != nil {
		cleanup()
		return func() {}, err
	}
	explorer := filepath.Join(os.Getenv("WINDIR"), "explorer.exe")
	if os.Getenv("WINDIR") == "" {
		explorer = `C:\Windows\explorer.exe`
	}
	// explorer.exe exits with 1 even when it worked, so only a failure to
	// start counts.
	if err := exec.Command(explorer, f).Start(); err != nil {
		cleanup()
		return func() {}, err
	}
	return cleanup, nil
}

// cleanupDelay is how long the shortcut is kept for explorer.exe to read.
const cleanupDelay = 15 * time.Second

// CleanupDelay tells callers how long to keep the shortcut.
func CleanupDelay() time.Duration { return cleanupDelay }
