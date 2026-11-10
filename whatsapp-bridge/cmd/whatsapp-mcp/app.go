package main

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/httpapi"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/logx"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/mcptools"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/panel"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/version"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/winsvc"
)

// parse accepts flags before, between and after positional arguments and
// returns the positional ones.
func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string
	for {
		if err := fs.Parse(args); err != nil {
			return nil, err
		}
		rest := fs.Args()
		if len(rest) == 0 {
			return pos, nil
		}
		pos = append(pos, rest[0])
		args = rest[1:]
	}
}

func isService() bool { return winsvc.IsService() }

func defaultConfigHint() string { return config.DefaultPath() }

// flags returns a FlagSet with the common --config flag.
func flags(name string) (*flag.FlagSet, *string) {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	cfg := fs.String("config", config.DefaultPath(), "config file")
	return fs, cfg
}

// app is everything that needs the database.
type app struct {
	cfg    *config.Config
	db     *sql.DB
	store  *mstore.Store
	policy *policy.Policy
	sink   *logx.Sink
	log    *logx.Logger
}

func openApp(ctx context.Context, cfgPath string, console io.Writer, wait time.Duration) (*app, error) {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("no se pudo leer la configuración (%w). ¿Está instalado? Si no, ejecuta el instalador", err)
	}
	sink, err := logx.NewSink(cfg.LogFile, console)
	if err != nil {
		sink, _ = logx.NewSink("", console)
	}
	log := logx.New(sink, "Main", cfg.LogLevel)
	d, err := db.Open(ctx, cfg.DatabaseURL, wait)
	if err != nil {
		sink.Close()
		return nil, err
	}
	if err := db.Migrate(ctx, d); err != nil {
		d.Close()
		sink.Close()
		return nil, fmt.Errorf("database schema: %w", err)
	}
	return &app{cfg: cfg, db: d, store: mstore.New(d), policy: policy.New(d), sink: sink, log: log}, nil
}

func (a *app) close() {
	a.db.Close()
	a.sink.Close()
}

func (a *app) bridge(ctx context.Context) (*wa.Bridge, error) {
	return wa.New(ctx, wa.Options{DB: a.db, Store: a.store, Policy: a.policy, MediaDir: a.cfg.MediaDir,
		FFmpegPath: a.cfg.FFmpegPath, DeviceName: a.cfg.DeviceName, Log: a.log})
}

func cmdRun(args []string) error {
	fs, cfgPath := flags("run")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if winsvc.IsService() {
		return winsvc.Run(serviceName, func(ctx context.Context) error { return serve(ctx, *cfgPath, nil) })
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return serve(ctx, *cfgPath, os.Stdout)
}

func serve(ctx context.Context, cfgPath string, console io.Writer) error {
	// On boot the database may take a while: wait up to 3 minutes.
	a, err := openApp(ctx, cfgPath, console, 3*time.Minute)
	if err != nil {
		return err
	}
	defer a.close()
	a.log.Infof("WhatsApp MCP %s starting", version.Version)
	if err := os.MkdirAll(a.cfg.MediaDir, 0o700); err != nil {
		return err
	}
	b, err := a.bridge(ctx)
	if err != nil {
		return err
	}
	go func() {
		// Keep trying to connect if the network is not up yet.
		for attempt := 0; ; attempt++ {
			if err := b.Start(); err == nil {
				return
			} else {
				a.log.Warnf("WhatsApp connect failed (will retry): %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(min(60, 5*(attempt+1))) * time.Second):
			}
		}
	}()
	defer b.Stop()
	if changed, err := a.cfg.EnsureCloudKey(); err != nil {
		return err
	} else if changed {
		if err := a.cfg.Save(a.cfg.Path()); err != nil {
			a.log.Warnf("could not save the new cloud key: %v", err)
		}
	}
	cloud := httpapi.NewCloud(a.cfg, mcptools.NewServer(mcptools.Deps{Store: a.store, Policy: a.policy, Bridge: b, Actor: policy.ActorCloud}), a.policy, a.log.Sub("Cloud"))
	pnl := panel.New(panel.Deps{Cfg: a.cfg, Store: a.store, Policy: a.policy, Bridge: b, Log: a.log.Sub("Panel"),
		TailscaleIPs: httpapi.TailscaleIPs, Cloud: cloud})
	srv, err := httpapi.New(a.cfg, mcptools.NewServer(mcptools.Deps{Store: a.store, Policy: a.policy, Bridge: b}), b, a.policy, pnl, a.log.Sub("HTTP"))
	if err != nil {
		return err
	}
	go cloud.Run(ctx)
	a.policy.Audit(ctx, policy.ActorSystem, "service.start", "", "ok", map[string]any{"version": version.Version})
	srv.Run(ctx)
	a.log.Infof("Stopping")
	return nil
}
