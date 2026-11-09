// Package pgsetup creates the private PostgreSQL instance used only by the
// WhatsApp connector: its own data folder, its own port, listening only on
// 127.0.0.1, password auth (SCRAM), its own Windows service, and an
// application user that owns the "whatsapp" database.
package pgsetup

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
)

// Logf prints progress.
type Logf func(format string, args ...any)

func exe(binDir, name string) string {
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(binDir, name)
}

func run(logf Logf, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", filepath.Base(name), strings.Join(redact(args), " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

func redact(args []string) []string {
	out := make([]string, len(args))
	for i, a := range args {
		if strings.HasPrefix(a, "--pwfile") || strings.Contains(strings.ToLower(a), "password") {
			out[i] = "***"
		} else {
			out[i] = a
		}
	}
	return out
}

// Initialized reports whether the data folder already holds a cluster.
func Initialized(dataDir string) bool {
	_, err := os.Stat(filepath.Join(dataDir, "PG_VERSION"))
	return err == nil
}

// InitCluster runs initdb and writes a locked-down configuration.
func InitCluster(m *config.ManagedPostgres, logf Logf) error {
	if Initialized(m.DataDir) {
		logf("PostgreSQL data folder already exists, keeping it: %s", m.DataDir)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(m.DataDir), 0o700); err != nil {
		return err
	}
	pw, err := os.CreateTemp("", "pgpw-*")
	if err != nil {
		return err
	}
	defer os.Remove(pw.Name())
	if _, err := pw.WriteString(m.SuperuserPassword + "\n"); err != nil {
		pw.Close()
		return err
	}
	pw.Close()
	// On Windows initdb drops administrator rights before it runs, so the
	// data folder must be writable by the installing user itself.
	cleanup, err := prepareDataDir(m, logf)
	if err != nil {
		return err
	}
	defer cleanup()
	logf("Creating the PostgreSQL database cluster (initdb)...")
	if err := run(logf, exe(m.BinDir, "initdb"), "-D", m.DataDir, "-U", "postgres", "--pwfile="+pw.Name(),
		"-A", "scram-sha-256", "-E", "UTF8", "--no-locale"); err != nil {
		return err
	}
	conf := fmt.Sprintf(`
# --- WhatsApp MCP: private instance, local only ---
listen_addresses = '127.0.0.1'
port = %d
password_encryption = scram-sha-256
max_connections = 30
shared_buffers = 128MB
logging_collector = on
log_directory = 'log'
log_filename = 'postgresql-%%a.log'
log_truncate_on_rotation = on
log_rotation_age = 1d
`, m.Port)
	if runtime.GOOS != "windows" {
		conf += fmt.Sprintf("unix_socket_directories = '%s'\n", filepath.ToSlash(m.DataDir))
	}
	f, err := os.OpenFile(filepath.Join(m.DataDir, "postgresql.conf"), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString(conf); err != nil {
		f.Close()
		return err
	}
	f.Close()
	hba := "# WhatsApp MCP: only password logins from this machine.\n" +
		"host all all 127.0.0.1/32 scram-sha-256\n" +
		"host all all ::1/128 scram-sha-256\n"
	if runtime.GOOS != "windows" {
		hba = "local all all scram-sha-256\n" + hba
	}
	return os.WriteFile(filepath.Join(m.DataDir, "pg_hba.conf"), []byte(hba), 0o600)
}

// SuperuserURL is the address used for setup only.
func SuperuserURL(m *config.ManagedPostgres, db string) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword("postgres", m.SuperuserPassword),
		Host: "127.0.0.1:" + strconv.Itoa(m.Port), Path: "/" + db, RawQuery: "sslmode=disable"}
	return u.String()
}

// AppURL is the address the connector uses.
func AppURL(m *config.ManagedPostgres) string {
	u := url.URL{Scheme: "postgres", User: url.UserPassword(m.AppUser, m.AppPassword),
		Host: "127.0.0.1:" + strconv.Itoa(m.Port), Path: "/" + m.Database, RawQuery: "sslmode=disable&application_name=whatsapp-mcp"}
	return u.String()
}

func quoteIdent(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }
func quoteLit(s string) string   { return `'` + strings.ReplaceAll(s, `'`, `''`) + `'` }

// WaitReady waits until the server accepts the superuser login.
func WaitReady(ctx context.Context, m *config.ManagedPostgres, wait time.Duration) error {
	d, err := sql.Open("pgx", SuperuserURL(m, "postgres"))
	if err != nil {
		return err
	}
	defer d.Close()
	deadline := time.Now().Add(wait)
	for {
		c, cancel := context.WithTimeout(ctx, 3*time.Second)
		err = d.PingContext(c)
		cancel()
		if err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("PostgreSQL did not start on port %d: %w", m.Port, err)
		}
		time.Sleep(time.Second)
	}
}

// EnsureAppDatabase creates (or updates the password of) the application
// user and the database it owns.
func EnsureAppDatabase(ctx context.Context, m *config.ManagedPostgres, logf Logf) error {
	d, err := sql.Open("pgx", SuperuserURL(m, "postgres"))
	if err != nil {
		return err
	}
	defer d.Close()
	var exists bool
	if err := d.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = $1)`, m.AppUser).Scan(&exists); err != nil {
		return err
	}
	if exists {
		_, err = d.ExecContext(ctx, `ALTER ROLE `+quoteIdent(m.AppUser)+` WITH LOGIN PASSWORD `+quoteLit(m.AppPassword))
	} else {
		logf("Creating database user %s...", m.AppUser)
		_, err = d.ExecContext(ctx, `CREATE ROLE `+quoteIdent(m.AppUser)+` WITH LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE PASSWORD `+quoteLit(m.AppPassword))
	}
	if err != nil {
		return err
	}
	if err := d.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)`, m.Database).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		logf("Creating database %s...", m.Database)
		if _, err := d.ExecContext(ctx, `CREATE DATABASE `+quoteIdent(m.Database)+` OWNER `+quoteIdent(m.AppUser)+` ENCODING 'UTF8' TEMPLATE template0`); err != nil {
			return err
		}
	}
	_, err = d.ExecContext(ctx, `REVOKE ALL ON DATABASE `+quoteIdent(m.Database)+` FROM PUBLIC`)
	if err != nil {
		return err
	}
	_, err = d.ExecContext(ctx, `GRANT ALL ON DATABASE `+quoteIdent(m.Database)+` TO `+quoteIdent(m.AppUser))
	return err
}

// Setup does everything: cluster, service, start, database.
func Setup(ctx context.Context, m *config.ManagedPostgres, logf Logf) error {
	if err := InitCluster(m, logf); err != nil {
		return err
	}
	if err := installAndStart(m, logf); err != nil {
		return err
	}
	if err := WaitReady(ctx, m, 60*time.Second); err != nil {
		return err
	}
	return EnsureAppDatabase(ctx, m, logf)
}

// Remove stops and unregisters the service (data is kept unless purge).
func Remove(m *config.ManagedPostgres, purge bool, logf Logf) error {
	err := stopAndRemove(m, logf)
	if purge {
		logf("Deleting PostgreSQL data: %s", m.DataDir)
		if e := os.RemoveAll(m.DataDir); e != nil && err == nil {
			err = e
		}
	}
	return err
}
