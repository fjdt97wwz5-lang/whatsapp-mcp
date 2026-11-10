//go:build windows

package wa

import "os/exec"

// openInBrowser opens a local file with the default program. Going through
// explorer.exe starts it as the normal (not elevated) user, like a
// double-click. explorer.exe returns 1 even on success, so the result is
// ignored.
func openInBrowser(path string) error {
	_ = exec.Command(`C:\Windows\explorer.exe`, path).Start()
	return nil
}
