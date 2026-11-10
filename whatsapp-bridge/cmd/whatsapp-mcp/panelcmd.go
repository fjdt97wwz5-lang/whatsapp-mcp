package main

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/panel"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/winsvc"
)

// cmdPanel opens the web panel in the browser with a one-time login link.
// Only an administrator can run it, so only an administrator gets in
// without typing the access key.
func cmdPanel(args []string) error {
	fs, cfgPath := flags("panel")
	open := fs.String("open", "", "section to open: vincular, conversaciones, lista, ajustes, registro, conectar, importar")
	if _, err := parse(fs, args); err != nil {
		return err
	}
	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("no se pudo leer la configuración (%w). ¿Está instalado?", err)
	}
	if _, err := liveStatus(cfg); err != nil && runtime.GOOS == "windows" {
		fmt.Println("Arrancando el servicio de WhatsApp MCP…")
		_ = winsvc.Start(pgServiceName)
		_ = winsvc.Start(serviceName)
		deadline := time.Now().Add(90 * time.Second)
		for {
			if _, err = liveStatus(cfg); err == nil || time.Now().After(deadline) {
				break
			}
			time.Sleep(2 * time.Second)
		}
	}
	if _, err := liveStatus(cfg); err != nil {
		return fmt.Errorf("el servicio no responde (%v). Mira \"Estado\" en el menú Inicio", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	d, err := db.Open(ctx, cfg.DatabaseURL, 10*time.Second)
	if err != nil {
		return err
	}
	defer d.Close()
	tok, err := panel.CreateLoginLink(ctx, d)
	if err != nil {
		return err
	}
	// The token goes after "#": browsers never send that part to the server,
	// so only the panel page itself can spend it.
	u := fmt.Sprintf("http://127.0.0.1:%d/panel/login#t=%s", cfg.ListenPort, tok)
	if *open != "" {
		u += "&s=" + *open
	}
	used := func() bool {
		c, cl := context.WithTimeout(context.Background(), 3*time.Second)
		defer cl()
		return panel.LinkUsed(c, d, tok)
	}

	fmt.Println("Abriendo el panel en tu navegador…")
	cleanup, oerr := wa.OpenURL(u)
	defer cleanup()
	if oerr == nil {
		for end := time.Now().Add(12 * time.Second); time.Now().Before(end); time.Sleep(time.Second) {
			if used() {
				fmt.Println("Panel abierto en el navegador.")
				return nil
			}
		}
	}

	fmt.Println("El navegador no abrió el panel. Copia esta dirección y pégala en Chrome o Edge en este servidor (vale 10 minutos, una sola vez):")
	fmt.Println()
	fmt.Println("  " + u)
	fmt.Println()
	win, werr := showLinkWindow(u)
	if werr != nil {
		fmt.Print("Cuando se abra el panel, esta ventana se cierra sola. También puedes presionar Enter.")
		enter := make(chan struct{})
		go func() { fmt.Scanln(); close(enter) }()
		for end := time.Now().Add(10 * time.Minute); time.Now().Before(end); {
			select {
			case <-enter:
				return nil
			case <-time.After(time.Second):
				if used() {
					fmt.Println("\nPanel abierto en el navegador.")
					return nil
				}
			}
		}
		return nil
	}
	fmt.Println("También está en la ventanita con el botón Copiar.")
	done := make(chan struct{})
	go func() { _ = win.Wait(); close(done) }()
	for end := time.Now().Add(10 * time.Minute); time.Now().Before(end); {
		select {
		case <-done:
			return nil
		case <-time.After(time.Second):
			if used() {
				_ = win.Process.Kill()
				fmt.Println("Panel abierto en el navegador.")
				return nil
			}
		}
	}
	_ = win.Process.Kill()
	return nil
}
