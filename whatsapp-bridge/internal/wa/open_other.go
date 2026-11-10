//go:build !windows

package wa

import (
	"os/exec"
	"runtime"
)

func openInBrowser(path string) error {
	if runtime.GOOS == "darwin" {
		return exec.Command("open", path).Start()
	}
	if p, err := exec.LookPath("xdg-open"); err == nil {
		return exec.Command(p, path).Start()
	}
	return nil
}
