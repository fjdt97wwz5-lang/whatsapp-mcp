package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/httpapi"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/pgsetup"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/proxy"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/secure"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/sqlitemigrate"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/version"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/winsvc"
)

const connectionFile = "CONECTAR-DESDE-MAC.txt"

func step(n, total int, msg string, args ...any) {
	fmt.Printf("[%d/%d] %s\n", n, total, fmt.Sprintf(msg, args...))
}

func cmdInstall(args []string) error {
	fs, cfgPath := flags("install")
	pgBin := fs.String("pg-bin", "", "folder with initdb/pg_ctl/postgres (bundled by the installer)")
	dataDir := fs.String("data-dir", config.ProgramDataDir(), "data folder")
	port := fs.Int("port", 8765, "MCP/REST port")
	pgPort := fs.Int("pg-port", 5544, "port of the private PostgreSQL")
	noService := fs.Bool("no-service", false, "do not install the Windows service (tests)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ctx := context.Background()
	const total = 7
	logf := func(f string, a ...any) { fmt.Printf("      "+f+"\n", a...) }

	step(1, total, "Carpeta de datos protegida: %s", *dataDir)
	if err := os.MkdirAll(*dataDir, 0o700); err != nil {
		return err
	}
	if err := secure.LockDown(*dataDir); err != nil {
		return fmt.Errorf("no se pudieron proteger los permisos de %s: %w", *dataDir, err)
	}

	step(2, total, "Configuración")
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("la configuración existente no es válida, no la toco: %w", err)
		}
		logf("Nueva instalación: generando claves en este equipo")
		if cfg, err = config.Defaults(*dataDir); err != nil {
			return err
		}
		cfg.ListenPort = *port
	} else {
		logf("Actualización: se conservan la clave, las contraseñas y los datos")
	}
	if cfg.ManagedPostgres == nil && cfg.DatabaseURL == "" {
		su, err := config.RandomSecret(24)
		if err != nil {
			return err
		}
		app, err := config.RandomSecret(24)
		if err != nil {
			return err
		}
		cfg.ManagedPostgres = &config.ManagedPostgres{
			DataDir: filepath.Join(*dataDir, "pgdata"), Port: *pgPort, ServiceName: pgServiceName,
			SuperuserPassword: su, AppUser: "whatsapp_app", AppPassword: app, Database: "whatsapp",
		}
	}

	if m := cfg.ManagedPostgres; m != nil {
		if *pgBin != "" {
			m.BinDir = *pgBin
		}
		if m.BinDir == "" {
			return errors.New("--pg-bin es obligatorio la primera vez")
		}
		step(3, total, "PostgreSQL privado solo para WhatsApp (puerto %d, solo 127.0.0.1)", m.Port)
		if err := pgsetup.Setup(ctx, m, logf); err != nil {
			return err
		}
		cfg.DatabaseURL = pgsetup.AppURL(m)
	} else {
		step(3, total, "Usando la base de datos de database_url (no administrada por el instalador)")
	}

	step(4, total, "Guardando configuración")
	if err := cfg.Save(*cfgPath); err != nil {
		return err
	}
	if err := secure.LockFile(*cfgPath); err != nil {
		return err
	}

	step(5, total, "Tablas de la base de datos")
	d, err := db.Open(ctx, cfg.DatabaseURL, 60*time.Second)
	if err != nil {
		return err
	}
	if err := db.Migrate(ctx, d); err != nil {
		d.Close()
		return err
	}
	pol := policy.New(d)
	if s, err := pol.Settings(ctx); err == nil && s.AttachmentsFolder == "" {
		folder := filepath.Join(*dataDir, "enviar")
		_ = os.MkdirAll(folder, 0o700)
		_, _, _ = pol.SetSetting(ctx, "attachments_folder", folder, policy.ActorInstaller)
	}
	pol.Audit(ctx, policy.ActorInstaller, "install", "", "ok", map[string]any{"version": version.Version})
	d.Close()
	for _, dir := range []string{cfg.MediaDir, filepath.Dir(cfg.LogFile)} {
		_ = os.MkdirAll(dir, 0o700)
	}

	if *noService {
		step(6, total, "Servicio de Windows omitido (--no-service)")
	} else {
		step(6, total, "Servicio de Windows %s (arranca solo con Windows y se reinicia si falla)", serviceName)
		exe, err := os.Executable()
		if err != nil {
			return err
		}
		_ = winsvc.Stop(serviceName)
		deps := []string{"Tcpip"}
		if cfg.ManagedPostgres != nil {
			deps = append(deps, cfg.ManagedPostgres.ServiceName)
		}
		if err := winsvc.Install(winsvc.Spec{Name: serviceName, DisplayName: "WhatsApp MCP",
			Description: "WhatsApp connector for Claude. Listens only on 127.0.0.1 and Tailscale, with an access token.",
			Exe:         exe, Args: []string{"run", "--config", *cfgPath}, DependsOn: deps}); err != nil {
			return err
		}
		if err := allowFirewall(exe, cfg.ListenPort); err != nil {
			logf("aviso: no se pudo abrir el puerto %d en el firewall para Tailscale: %v", cfg.ListenPort, err)
		} else {
			logf("Firewall: puerto %d abierto solo para direcciones de Tailscale", cfg.ListenPort)
		}
		if err := winsvc.Start(serviceName); err != nil {
			return fmt.Errorf("el servicio no arrancó: %w", err)
		}
	}

	step(7, total, "Datos para conectar desde tu Mac")
	info := filepath.Join(*dataDir, connectionFile)
	if err := os.WriteFile(info, []byte(connectionInfo(cfg)), 0o600); err != nil {
		return err
	}
	_ = secure.LockFile(info)
	fmt.Printf("      Guardado en %s (solo administradores pueden abrirlo)\n\nListo. Siguiente paso: abre \"Vincular WhatsApp\" en el menú Inicio.\n", info)
	return nil
}

func cmdUninstall(args []string) error {
	fs, cfgPath := flags("uninstall")
	purge := fs.Bool("purge", false, "delete messages, session and settings too")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	fmt.Println("Deteniendo y quitando el servicio", serviceName)
	if err := winsvc.Remove(serviceName); err != nil {
		fmt.Println("  aviso:", err)
	}
	removeFirewall()
	cfg, err := config.Load(*cfgPath)
	if err == nil && cfg.ManagedPostgres != nil {
		if err := pgsetup.Remove(cfg.ManagedPostgres, *purge, func(f string, a ...any) { fmt.Printf("  "+f+"\n", a...) }); err != nil {
			fmt.Println("  aviso:", err)
		}
	}
	if *purge {
		dir := filepath.Dir(*cfgPath)
		fmt.Println("Borrando", dir)
		return os.RemoveAll(dir)
	}
	fmt.Println("Los datos se conservan en", filepath.Dir(*cfgPath))
	return nil
}

// withServiceStopped stops the background service for commands that need
// the WhatsApp session for themselves, and starts it again afterwards.
func withServiceStopped(fn func() error) error {
	wasRunning := winsvc.State(serviceName) == "running"
	if wasRunning {
		fmt.Println("Deteniendo el servicio un momento...")
		if err := winsvc.Stop(serviceName); err != nil {
			return err
		}
	}
	err := fn()
	if wasRunning || winsvc.State(serviceName) == "stopped" {
		fmt.Println("Arrancando el servicio otra vez...")
		if e := winsvc.Start(serviceName); e != nil && err == nil {
			err = e
		}
	}
	return err
}

func cmdPair(args []string) error {
	fs, cfgPath := flags("pair")
	phone := fs.String("phone", "", "link with an 8 character code instead of a QR (full number with country code)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	return withServiceStopped(func() error {
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		a, err := openApp(ctx, *cfgPath, nil, 30*time.Second)
		if err != nil {
			return err
		}
		defer a.close()
		b, err := a.bridge(ctx)
		if err != nil {
			return err
		}
		if b.Paired() {
			fmt.Println("Ya hay un WhatsApp vinculado (" + b.Status().Account + "). Para cambiarlo usa primero: whatsapp-mcp unlink")
			return nil
		}
		fmt.Println("Recomendación: usa un número secundario, no el principal de la empresa (WhatsApp no permite clientes no oficiales).")
		pctx, cancel := context.WithTimeout(ctx, 4*time.Minute)
		defer cancel()
		err = b.Pair(pctx, os.Stdout, *phone)
		if err == nil {
			a.policy.Audit(ctx, policy.ActorManual, "session.paired", b.Status().Account, "ok", nil)
			fmt.Println("Esperando unos segundos a que llegue el historial...")
			time.Sleep(10 * time.Second)
		}
		b.Stop()
		return err
	})
}

func cmdUnlink(args []string) error {
	fs, cfgPath := flags("unlink")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if !confirm("¿Desvincular WhatsApp de este servidor? (s/n): ") {
		return nil
	}
	return withServiceStopped(func() error {
		ctx := context.Background()
		a, err := openApp(ctx, *cfgPath, nil, 30*time.Second)
		if err != nil {
			return err
		}
		defer a.close()
		b, err := a.bridge(ctx)
		if err != nil {
			return err
		}
		if err := b.Unlink(ctx); err != nil {
			return err
		}
		a.policy.Audit(ctx, policy.ActorManual, "session.unlinked", "", "ok", nil)
		fmt.Println("Desvinculado. El historial guardado se conserva.")
		return nil
	})
}

func cmdStatus(args []string) error {
	fs, cfgPath := flags("status")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	fmt.Println("Versión:", version.Version)
	if runtime.GOOS == "windows" {
		fmt.Println("Servicio", serviceName+":", winsvc.State(serviceName))
		fmt.Println("Servicio", pgServiceName+":", winsvc.State(pgServiceName))
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Println(whatsappLine(cfg))
	ts := httpapi.TailscaleIPs()
	if len(ts) == 0 {
		fmt.Println("Tailscale: no veo direcciones de Tailscale en este equipo (¿está conectado?)")
	} else {
		for _, ip := range ts {
			fmt.Printf("Tailscale: http://%s/mcp\n", net.JoinHostPort(ip.String(), strconv.Itoa(cfg.ListenPort)))
		}
	}
	ctx := context.Background()
	d, err := db.Open(ctx, cfg.DatabaseURL, 5*time.Second)
	if err != nil {
		fmt.Println("Base de datos: NO responde:", err)
		return nil
	}
	defer d.Close()
	var msgs, chats, approved int
	_ = d.QueryRowContext(ctx, `SELECT count(*) FROM wa_messages`).Scan(&msgs)
	_ = d.QueryRowContext(ctx, `SELECT count(*) FROM wa_chats`).Scan(&chats)
	_ = d.QueryRowContext(ctx, `SELECT count(*) FROM approved_recipients`).Scan(&approved)
	var paired bool
	_ = d.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM whatsmeow_device)`).Scan(&paired)
	fmt.Printf("Base de datos: OK (%d chats, %d mensajes)\nWhatsApp vinculado: %v\nLista aprobada: %d contactos/grupos\n", chats, msgs, paired, approved)
	return nil
}

func cmdMigrate(args []string) error {
	fs, cfgPath := flags("migrate-sqlite")
	from := fs.String("from", "", "path of messages.db copied from the Mac")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	if *from == "" {
		return errors.New("falta --from RUTA\\messages.db")
	}
	ctx := context.Background()
	a, err := openApp(ctx, *cfgPath, nil, 30*time.Second)
	if err != nil {
		return err
	}
	defer a.close()
	start := time.Now()
	fmt.Println("Importando", *from, "...")
	res, err := sqlitemigrate.Import(ctx, *from, a.db, func(done, total int) {
		pct := 100
		if total > 0 {
			pct = done * 100 / total
		}
		fmt.Printf("\r  %3d%%  %d de %d mensajes", pct, done, total)
	})
	fmt.Println()
	if err != nil {
		a.policy.Audit(ctx, policy.ActorManual, "history.import", *from, "error", map[string]any{"error": err.Error()})
		return err
	}
	a.policy.Audit(ctx, policy.ActorManual, "history.import", *from, "ok", map[string]any{
		"chats_read": res.ChatsRead, "messages_read": res.MessagesRead, "messages_imported": res.MessagesImported})
	fmt.Printf("Listo en %s: %d chats leídos, %d mensajes leídos, %d nuevos importados, %d sin fecha omitidos (los repetidos se saltan).\n",
		time.Since(start).Round(time.Second), res.ChatsRead, res.MessagesRead, res.MessagesImported, res.MessagesSkipped)
	return nil
}

func cmdSettings(args []string) error {
	fs, cfgPath := flags("settings")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	a, err := openApp(ctx, *cfgPath, nil, 15*time.Second)
	if err != nil {
		return err
	}
	defer a.close()
	if len(rest) >= 3 && rest[0] == "set" {
		old, nv, err := a.policy.SetSetting(ctx, rest[1], strings.Join(rest[2:], " "), policy.ActorManual)
		if err != nil {
			return err
		}
		fmt.Printf("%s: %v -> %v (registrado)\n", rest[1], old, nv)
		return nil
	}
	if len(rest) > 0 {
		return errors.New("uso: settings  |  settings set CLAVE VALOR")
	}
	vs, err := a.policy.ListSettings(ctx)
	if err != nil {
		return err
	}
	for _, v := range vs {
		lock := ""
		if v.OnlyByHand {
			lock = "  [solo a mano]"
		}
		fmt.Printf("%-26s = %-8v %s%s\n", v.Key, v.Value, v.Description, lock)
	}
	return nil
}

func cmdAllow(args []string) error {
	fs, cfgPath := flags("allow")
	rest, err := parse(fs, args)
	if err != nil {
		return err
	}
	ctx := context.Background()
	a, err := openApp(ctx, *cfgPath, nil, 15*time.Second)
	if err != nil {
		return err
	}
	defer a.close()
	if len(rest) == 0 || rest[0] == "list" {
		rs, err := a.policy.ListApproved(ctx)
		if err != nil {
			return err
		}
		if len(rs) == 0 {
			fmt.Println("La lista aprobada está vacía: no se puede enviar a nadie todavía.")
		}
		for _, r := range rs {
			fmt.Printf("%-8s %-32s %-24s añadido %s por %s\n", r.Kind, r.JID, r.Label, r.AddedAt.Local().Format("2006-01-02 15:04"), r.AddedBy)
		}
		return nil
	}
	switch rest[0] {
	case "add":
		if len(rest) < 2 {
			return errors.New("uso: allow add NUMERO_O_JID [ETIQUETA]")
		}
		r, err := a.policy.AddApproved(ctx, rest[1], strings.Join(rest[2:], " "), policy.ActorManual)
		if err != nil {
			return err
		}
		fmt.Printf("Añadido: %s (%s) %s\n", r.JID, r.Kind, r.Label)
	case "remove":
		if len(rest) < 2 {
			return errors.New("uso: allow remove NUMERO_O_JID")
		}
		ok, err := a.policy.RemoveApproved(ctx, rest[1], policy.ActorManual)
		if err != nil {
			return err
		}
		if ok {
			fmt.Println("Quitado de la lista.")
		} else {
			fmt.Println("No estaba en la lista.")
		}
	default:
		return errors.New("uso: allow list | add NUMERO_O_JID [ETIQUETA] | remove NUMERO_O_JID")
	}
	return nil
}

func cmdAudit(args []string) error {
	fs, cfgPath := flags("audit")
	limit := fs.Int("limit", 50, "entries")
	action := fs.String("action", "", "filter by action prefix")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	ctx := context.Background()
	a, err := openApp(ctx, *cfgPath, nil, 15*time.Second)
	if err != nil {
		return err
	}
	defer a.close()
	es, err := a.policy.ListAudit(ctx, *limit, *action)
	if err != nil {
		return err
	}
	for _, e := range es {
		d, _ := json.Marshal(e.Details)
		fmt.Printf("%s  %-9s %-20s %-30s %-8s %s\n", e.At.Local().Format("2006-01-02 15:04:05"), e.Actor, e.Action, e.Target, e.Outcome, d)
	}
	return nil
}

func connectionInfo(cfg *config.Config) string {
	host := "IP-DE-TAILSCALE-DEL-SERVIDOR"
	for _, ip := range httpapi.TailscaleIPs() {
		if ip.To4() != nil {
			host = ip.String()
			break
		}
	}
	url := fmt.Sprintf("http://%s:%d/mcp", host, cfg.ListenPort)
	return fmt.Sprintf(`Conector de WhatsApp: cómo conectar Claude desde tu Mac (por Tailscale)
========================================================================

Dirección MCP:  %[1]s
Clave (token):  %[2]s

Guarda la clave en un sitio seguro. Quien la tenga y esté en tu Tailscale
puede leer tus chats y enviar a tu lista aprobada. Si se filtra:
"whatsapp-mcp rotate-token" en el servidor.

Claude Code (terminal de la Mac):
  claude mcp add --transport http whatsapp %[1]s --header "Authorization: Bearer %[2]s"

Claude Desktop (Mac): copia el programa whatsapp-mcp para Mac a /usr/local/bin
y agrega esto en ~/Library/Application Support/Claude/claude_desktop_config.json:
{
  "mcpServers": {
    "whatsapp": {
      "command": "/usr/local/bin/whatsapp-mcp",
      "args": ["connect", "--url", "%[1]s"],
      "env": { "WHATSAPP_MCP_TOKEN": "%[2]s" }
    }
  }
}

Prueba rápida desde la Mac:
  curl -H "Authorization: Bearer %[2]s" http://%[3]s:%[4]d/api/health
`, url, cfg.AuthToken, host, cfg.ListenPort)
}

func cmdConnectionInfo(args []string) error {
	fs, cfgPath := flags("connection-info")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	fmt.Print(connectionInfo(cfg))
	return nil
}

func cmdRotateToken(args []string) error {
	fs, cfgPath := flags("rotate-token")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return err
	}
	if cfg.AuthToken, err = config.RandomSecret(32); err != nil {
		return err
	}
	if err := cfg.Save(*cfgPath); err != nil {
		return err
	}
	_ = secure.LockFile(*cfgPath)
	info := filepath.Join(filepath.Dir(*cfgPath), connectionFile)
	_ = os.WriteFile(info, []byte(connectionInfo(cfg)), 0o600)
	_ = secure.LockFile(info)
	if d, err := db.Open(context.Background(), cfg.DatabaseURL, 5*time.Second); err == nil {
		policy.New(d).Audit(context.Background(), policy.ActorManual, "token.rotate", "", "ok", nil)
		d.Close()
	}
	if winsvc.State(serviceName) == "running" {
		_ = winsvc.Stop(serviceName)
		_ = winsvc.Start(serviceName)
	}
	fmt.Println("Clave nueva generada. Actualízala en tu Mac (whatsapp-mcp connection-info la muestra).")
	return nil
}

func cmdConnect(args []string) error {
	fs := flag.NewFlagSet("connect", flag.ContinueOnError)
	url := fs.String("url", os.Getenv("WHATSAPP_MCP_URL"), "server MCP URL, e.g. http://100.x.y.z:8765/mcp")
	tokenFile := fs.String("token-file", "", "file with the token (else WHATSAPP_MCP_TOKEN)")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	token := os.Getenv("WHATSAPP_MCP_TOKEN")
	if *tokenFile != "" {
		b, err := os.ReadFile(*tokenFile)
		if err != nil {
			return err
		}
		token = strings.TrimSpace(string(b))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return proxy.Run(ctx, *url, token, os.Stdin, os.Stdout)
}

func cmdVersion([]string) error {
	fmt.Println("whatsapp-mcp", version.Version, runtime.GOOS+"/"+runtime.GOARCH)
	return nil
}
