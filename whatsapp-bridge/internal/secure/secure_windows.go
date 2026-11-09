//go:build windows

// Package secure restricts who can read the connector's data folder.
package secure

import (
	"fmt"
	"os/exec"
	"strings"
)

func icacls(args ...string) error {
	out, err := exec.Command(`C:\Windows\System32\icacls.exe`, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("icacls %s: %v %s", strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// LockDown removes inherited permissions on a folder and leaves full
// control only to SYSTEM and Administrators (well-known SIDs, so it works
// on Spanish Windows too). Sub-folders inherit it.
func LockDown(path string) error {
	if err := icacls(path, "/inheritance:r", "/Q"); err != nil {
		return err
	}
	return icacls(path, "/grant:r", "*S-1-5-18:(OI)(CI)F", "*S-1-5-32-544:(OI)(CI)F", "/Q")
}

// LockFile does the same for a single file.
func LockFile(path string) error {
	if err := icacls(path, "/inheritance:r", "/Q"); err != nil {
		return err
	}
	return icacls(path, "/grant:r", "*S-1-5-18:F", "*S-1-5-32-544:F", "/Q")
}
