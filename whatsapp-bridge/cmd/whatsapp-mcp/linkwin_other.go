//go:build !windows

package main

import (
	"errors"
	"os/exec"
)

func showLinkWindow(string) (*exec.Cmd, error) {
	return nil, errors.New("sin ventana en este sistema")
}
