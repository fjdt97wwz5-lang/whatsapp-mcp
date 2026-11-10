//go:build !windows

package wa

import "time"

// OpenURL opens a web address in the default browser.
func OpenURL(u string) (cleanup func(), err error) {
	return func() {}, openInBrowser(u)
}

// CleanupDelay tells callers how long to keep the shortcut (none here).
func CleanupDelay() time.Duration { return 0 }
