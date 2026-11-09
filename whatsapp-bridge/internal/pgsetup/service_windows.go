//go:build windows

package pgsetup

import (
	"fmt"
	"os"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
)

const pgServiceAccount = `NT AUTHORITY\NetworkService`

// prepareDataDir creates the empty data folder and lets both the database
// account (NetworkService) and the installing user write to it. initdb runs
// with a restricted token where the Administrators group no longer counts.
// The user's own grant is removed again once initdb has finished.
func prepareDataDir(m *config.ManagedPostgres, logf Logf) (func(), error) {
	if err := os.MkdirAll(m.DataDir, 0o700); err != nil {
		return nil, err
	}
	grants := []string{m.DataDir, "/grant", "*S-1-5-20:(OI)(CI)F"}
	sid := currentUserSID()
	temporary := sid != "" && sid != "S-1-5-18"
	if temporary {
		grants = append(grants, "*"+sid+":(OI)(CI)F")
	}
	grants = append(grants, "/Q")
	if err := run(logf, `C:\Windows\System32\icacls.exe`, grants...); err != nil {
		return nil, err
	}
	return func() {
		if temporary {
			_ = run(logf, `C:\Windows\System32\icacls.exe`, m.DataDir, "/remove:g", "*"+sid, "/T", "/Q")
		}
	}, nil
}

func currentUserSID() string {
	u, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return ""
	}
	return u.User.Sid.String()
}

func installAndStart(m *config.ManagedPostgres, logf Logf) error {
	// The database runs as the low-privilege NetworkService account, which
	// needs full control of its own data folder (and nothing else).
	logf("Giving the PostgreSQL service account access to its data folder...")
	if err := run(logf, `C:\Windows\System32\icacls.exe`, m.DataDir, "/grant", "*S-1-5-20:(OI)(CI)F", "/T", "/Q"); err != nil {
		return err
	}
	sm, err := mgr.Connect()
	if err != nil {
		return err
	}
	s, err := sm.OpenService(m.ServiceName)
	if err != nil {
		sm.Disconnect()
		logf("Registering Windows service %s...", m.ServiceName)
		if err := run(logf, exe(m.BinDir, "pg_ctl"), "register", "-N", m.ServiceName, "-U", pgServiceAccount,
			"-D", m.DataDir, "-S", "auto", "-w"); err != nil {
			return err
		}
		if sm, err = mgr.Connect(); err != nil {
			return err
		}
		if s, err = sm.OpenService(m.ServiceName); err != nil {
			sm.Disconnect()
			return err
		}
		cfg, err := s.Config()
		if err == nil {
			cfg.DisplayName = "WhatsApp MCP - PostgreSQL"
			cfg.Description = "Private PostgreSQL instance used only by the WhatsApp MCP connector (127.0.0.1 only)."
			_ = s.UpdateConfig(cfg)
		}
		_ = s.SetRecoveryActions([]mgr.RecoveryAction{
			{Type: mgr.ServiceRestart, Delay: 10 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
			{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
		}, 86400)
	}
	defer sm.Disconnect()
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return err
	}
	if st.State == svc.Running {
		return nil
	}
	logf("Starting service %s...", m.ServiceName)
	if err := s.Start(); err != nil && err != windows.ERROR_SERVICE_ALREADY_RUNNING {
		return fmt.Errorf("start %s: %w", m.ServiceName, err)
	}
	return nil
}

func stopAndRemove(m *config.ManagedPostgres, logf Logf) error {
	sm, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer sm.Disconnect()
	s, err := sm.OpenService(m.ServiceName)
	if err != nil {
		return nil // not installed
	}
	defer s.Close()
	logf("Stopping and removing service %s...", m.ServiceName)
	if st, err := s.Control(svc.Stop); err == nil {
		for i := 0; i < 60 && st.State != svc.Stopped; i++ {
			time.Sleep(500 * time.Millisecond)
			if st, err = s.Query(); err != nil {
				break
			}
		}
	}
	return s.Delete()
}
