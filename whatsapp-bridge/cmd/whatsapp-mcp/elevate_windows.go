//go:build windows

package main

import (
	"os"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// isAdmin reports whether the process runs with an elevated token.
func isAdmin() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}

// relaunchElevated starts this program again "as administrator" (the UAC
// prompt) in a new window, with --pause so its output stays readable.
func relaunchElevated(args []string) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	quoted := make([]string, 0, len(args)+1)
	for _, a := range append(args, "--pause") {
		quoted = append(quoted, syscall.EscapeArg(a))
	}
	verb, _ := windows.UTF16PtrFromString("runas")
	file, _ := windows.UTF16PtrFromString(exe)
	params, _ := windows.UTF16PtrFromString(strings.Join(quoted, " "))
	return windows.ShellExecute(0, verb, file, params, nil, windows.SW_SHOWNORMAL)
}
