//go:build !windows

// Package winsvc: on systems other than Windows there is no service
// manager integration; the connector runs in the foreground.
package winsvc

import (
	"context"
	"errors"
)

var errNotWindows = errors.New("Windows services are only available on Windows")

func IsService() bool { return false }

func Run(string, func(ctx context.Context) error) error { return errNotWindows }

type Spec struct {
	Name, DisplayName, Description string
	Exe                            string
	Args                           []string
	DependsOn                      []string
}

func Install(Spec) error  { return errNotWindows }
func Remove(string) error { return errNotWindows }
func Start(string) error  { return errNotWindows }
func Stop(string) error   { return nil }
func State(string) string { return "not available" }
