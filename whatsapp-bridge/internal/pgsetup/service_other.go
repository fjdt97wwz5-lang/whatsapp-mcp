//go:build !windows

package pgsetup

import (
	"path/filepath"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
)

// On Linux/macOS (used for development and tests) the instance is started
// with pg_ctl instead of being registered as a service.
func installAndStart(m *config.ManagedPostgres, logf Logf) error {
	if run(logf, exe(m.BinDir, "pg_ctl"), "status", "-D", m.DataDir) == nil {
		return nil
	}
	logf("Starting PostgreSQL with pg_ctl...")
	return run(logf, exe(m.BinDir, "pg_ctl"), "start", "-w", "-D", m.DataDir, "-l", filepath.Join(m.DataDir, "startup.log"))
}

func stopAndRemove(m *config.ManagedPostgres, logf Logf) error {
	return run(logf, exe(m.BinDir, "pg_ctl"), "stop", "-w", "-m", "fast", "-D", m.DataDir)
}

// prepareDataDir is only needed on Windows; initdb creates the folder here.
func prepareDataDir(m *config.ManagedPostgres, logf Logf) (func(), error) {
	return func() {}, nil
}
