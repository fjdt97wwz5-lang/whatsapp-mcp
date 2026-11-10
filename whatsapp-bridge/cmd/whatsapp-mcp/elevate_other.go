//go:build !windows

package main

import "os"

func isAdmin() bool { return os.Geteuid() == 0 }

func relaunchElevated([]string, string) error { return nil }
