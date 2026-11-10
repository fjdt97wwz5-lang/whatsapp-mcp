// Command whatsapp-mcp is the WhatsApp connector for Claude: one program
// that keeps the WhatsApp session, stores messages in PostgreSQL, enforces
// the approved list, and serves MCP over HTTP to Tailscale clients with a
// token. Run "whatsapp-mcp help" for the commands.
package main

import (
	"fmt"
	"os"
	"runtime"
	"strings"
)

const (
	serviceName   = "WhatsAppMCP"
	pgServiceName = "WhatsAppMCP-Postgres"
)

type command struct {
	name, usage, help string
	run               func(args []string) error
	noAdmin           bool // runs without elevation (Mac side, version)
}

var commands []command

func init() {
	commands = []command{
		{"run", "run [--config FILE]", "Arranca el conector (lo usa el servicio de Windows).", cmdRun, false},
		{"install", "install --pg-bin DIR [--data-dir DIR] [--port 8765] [--pg-port 5544]", "Instala o actualiza: Postgres privado, base de datos, servicio que arranca solo (lo usa el instalador).", cmdInstall, false},
		{"uninstall", "uninstall [--purge]", "Quita los servicios. Con --purge borra también los datos.", cmdUninstall, false},
		{"pair", "pair [--phone +57...]", "Vincula tu WhatsApp (código QR, o código de 8 letras con --phone).", cmdPair, false},
		{"unlink", "unlink", "Desvincula WhatsApp de este servidor.", cmdUnlink, false},
		{"status", "status", "Muestra el estado: servicios, base de datos, WhatsApp, direcciones.", cmdStatus, false},
		{"migrate-sqlite", "migrate-sqlite --from RUTA\\messages.db", "Importa el historial del puente anterior (SQLite) a Postgres. Se puede repetir sin duplicar.", cmdMigrate, false},
		{"settings", "settings [set CLAVE VALOR]", "Muestra o cambia los ajustes (cada cambio queda registrado).", cmdSettings, false},
		{"allow", "allow list | add NUMERO_O_JID [ETIQUETA] | remove NUMERO_O_JID", "Lista aprobada de contactos y grupos a los que se puede enviar.", cmdAllow, false},
		{"audit", "audit [--limit 50] [--action message.send]", "Registro de envíos y cambios.", cmdAudit, false},
		{"connection-info", "connection-info", "Muestra cómo conectar Claude desde tu Mac por Tailscale.", cmdConnectionInfo, false},
		{"rotate-token", "rotate-token", "Genera una clave nueva (la anterior deja de funcionar).", cmdRotateToken, false},
		{"panel", "panel [--open vincular|conversaciones|lista|ajustes|registro|conectar]", "Abre el panel web en el navegador de este servidor.", cmdPanel, false},
		{"menu", "menu", "Menú en modo texto para hacer todo lo anterior.", cmdMenu, false},
		{"connect", "connect --url http://IP:8765/mcp  (clave en WHATSAPP_MCP_TOKEN)", "Para la Mac: conecta Claude Desktop (stdio) con el servidor.", cmdConnect, true},
		{"version", "version", "Muestra la versión.", cmdVersion, true},
	}
}

func usage() {
	fmt.Println("whatsapp-mcp: conector de WhatsApp para Claude\n\nComandos:")
	for _, c := range commands {
		fmt.Printf("  %-18s %s\n  %-18s   %s\n", c.name, c.help, "", c.usage)
	}
	fmt.Println("\nTodos aceptan --config RUTA (por defecto", defaultConfigHint()+").")
}

func main() {
	args := os.Args[1:]
	// The service manager starts us with "run"; a double-click starts the menu.
	if len(args) == 0 {
		if isService() {
			args = []string{"run"}
		} else {
			args = []string{"menu"}
		}
	}
	// --pause keeps the window open at the end (Start Menu shortcuts).
	// --pause-on-error only when something went wrong (the panel shortcut).
	pause, pauseOnError := false, false
	kept := args[:0:0]
	for _, a := range args {
		if a == "--pause" {
			pause = true
			continue
		}
		if a == "--pause-on-error" {
			pauseOnError = true
			continue
		}
		kept = append(kept, a)
	}
	args = kept
	if len(args) == 0 {
		args = []string{"menu"}
	}
	name := strings.ToLower(args[0])
	if name == "help" || name == "-h" || name == "--help" {
		usage()
		return
	}
	for _, c := range commands {
		if c.name == name {
			// The data folder belongs to SYSTEM and Administrators, so on the
			// server every command except the Mac ones needs elevation.
			if runtime.GOOS == "windows" && !c.noAdmin && !isAdmin() && !isService() {
				fmt.Println("Hace falta permiso de administrador; Windows te lo va a pedir...")
				pauseFlag := "--pause"
				if name == "panel" {
					pauseFlag = "--pause-on-error"
				}
				if err := relaunchElevated(args, pauseFlag); err != nil {
					fmt.Fprintln(os.Stderr, "Error:", err)
					os.Exit(1)
				}
				return
			}
			err := c.run(args[1:])
			if err != nil {
				fmt.Fprintln(os.Stderr, "Error:", err)
			}
			if pause || (pauseOnError && err != nil) {
				fmt.Print("\nPresiona Enter para cerrar esta ventana...")
				fmt.Scanln()
			}
			if err != nil {
				os.Exit(1)
			}
			return
		}
	}
	fmt.Fprintf(os.Stderr, "Comando desconocido %q\n\n", name)
	usage()
	os.Exit(2)
}
