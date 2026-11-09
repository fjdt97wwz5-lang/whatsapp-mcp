//go:build !windows

// Package secure restricts who can read the connector's data folder.
package secure

import "os"

// LockDown makes path readable only by its owner.
func LockDown(path string) error { return os.Chmod(path, 0o700) }

// LockFile makes a file readable only by its owner.
func LockFile(path string) error { return os.Chmod(path, 0o600) }
