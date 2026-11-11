//go:build windows

package transcribe

import (
	"os/exec"
	"syscall"
)

// hideWindow keeps a console window from flashing up on the server.
func hideWindow(c *exec.Cmd) {
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000} // CREATE_NO_WINDOW
}
