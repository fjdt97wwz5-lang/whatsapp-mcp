//go:build windows

// Package winsvc runs the connector as a Windows service and installs it
// so it starts by itself with Windows and restarts if it ever stops.
package winsvc

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

// IsService reports whether the process was started by the service manager.
func IsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

type handler struct {
	run func(ctx context.Context) error
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			cancel()
			if err != nil {
				return true, 1 // non-zero exit => recovery actions restart us
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				select {
				case <-done:
				case <-time.After(20 * time.Second):
				}
				return false, 0
			}
		}
	}
}

// Run hands control to the service manager.
func Run(name string, run func(ctx context.Context) error) error {
	return svc.Run(name, &handler{run: run})
}

// Spec describes the service to install.
type Spec struct {
	Name, DisplayName, Description string
	Exe                            string
	Args                           []string
	DependsOn                      []string
}

// Install creates or updates the service (automatic, delayed start, restart on failure).
func Install(s Spec) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	cfg := mgr.Config{
		DisplayName:      s.DisplayName,
		Description:      s.Description,
		StartType:        mgr.StartAutomatic,
		DelayedAutoStart: true,
		Dependencies:     s.DependsOn,
		ErrorControl:     mgr.ErrorNormal,
	}
	svcH, err := m.OpenService(s.Name)
	if err == nil {
		defer svcH.Close()
		cur, err := svcH.Config()
		if err != nil {
			return err
		}
		cur.DisplayName, cur.Description, cur.StartType = cfg.DisplayName, cfg.Description, cfg.StartType
		cur.DelayedAutoStart, cur.Dependencies = true, s.DependsOn
		cur.BinaryPathName = quoteCmd(s.Exe, s.Args)
		if err := svcH.UpdateConfig(cur); err != nil {
			return err
		}
	} else {
		svcH, err = m.CreateService(s.Name, s.Exe, cfg, s.Args...)
		if err != nil {
			return err
		}
		defer svcH.Close()
	}
	if err := svcH.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 15 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 60 * time.Second},
	}, 86400); err != nil {
		return err
	}
	return svcH.SetRecoveryActionsOnNonCrashFailures(true)
}

func quoteCmd(exe string, args []string) string {
	s := windows.EscapeArg(exe)
	for _, a := range args {
		s += " " + windows.EscapeArg(a)
	}
	return s
}

// Remove stops and deletes the service.
func Remove(name string) error {
	_ = Stop(name)
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return nil
	}
	defer s.Close()
	return s.Delete()
}

// Start starts the service.
func Start(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return err
	}
	defer s.Close()
	if err := s.Start(); err != nil && !errors.Is(err, windows.ERROR_SERVICE_ALREADY_RUNNING) {
		return err
	}
	return nil
}

// Stop stops the service and waits up to 30 seconds.
func Stop(name string) error {
	m, err := mgr.Connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return nil
	}
	defer s.Close()
	st, err := s.Control(svc.Stop)
	if err != nil {
		if errors.Is(err, windows.ERROR_SERVICE_NOT_ACTIVE) {
			return nil
		}
		return err
	}
	for i := 0; i < 60 && st.State != svc.Stopped; i++ {
		time.Sleep(500 * time.Millisecond)
		if st, err = s.Query(); err != nil {
			return err
		}
	}
	if st.State != svc.Stopped {
		return fmt.Errorf("service %s did not stop", name)
	}
	return nil
}

// State returns "running", "stopped", "not installed", etc.
func State(name string) string {
	m, err := mgr.Connect()
	if err != nil {
		return "unknown"
	}
	defer m.Disconnect()
	s, err := m.OpenService(name)
	if err != nil {
		return "not installed"
	}
	defer s.Close()
	st, err := s.Query()
	if err != nil {
		return "unknown"
	}
	switch st.State {
	case svc.Running:
		return "running"
	case svc.Stopped:
		return "stopped"
	case svc.StartPending:
		return "starting"
	case svc.StopPending:
		return "stopping"
	}
	return "unknown"
}
