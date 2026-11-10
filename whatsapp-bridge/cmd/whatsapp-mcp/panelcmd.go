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
	u := fmt.Sprintf("http://127.0.0.1:%d/panel/login?t=%s", cfg.ListenPort, tok)
	if *open != "" {
		u += "&s=" + *open
	}
	if err := wa.OpenInBrowser(u); err != nil {
		fmt.Println("No pude abrir el navegador. Abre esta dirección en este servidor (vale 2 minutos, una sola vez):")
		fmt.Println(u)
		return nil
	}
	fmt.Println("Panel abierto en el navegador.")
	return nil
}
