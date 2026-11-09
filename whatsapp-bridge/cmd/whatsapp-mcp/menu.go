package main

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

var stdin = bufio.NewReader(os.Stdin)

func ask(prompt string) string {
	fmt.Print(prompt)
	s, _ := stdin.ReadString('\n')
	return strings.TrimSpace(s)
}

func confirm(prompt string) bool {
	a := strings.ToLower(ask(prompt))
	return a == "s" || a == "si" || a == "sí" || a == "y" || a == "yes"
}

func cmdMenu(args []string) error {
	extra := args // passes --config through
	for {
		fmt.Print(`
================ WhatsApp MCP ================
 1. Estado
 2. Vincular WhatsApp (código QR)
 3. Vincular WhatsApp con código de 8 letras
 4. Ver lista aprobada
 5. Añadir a la lista aprobada
 6. Quitar de la lista aprobada
 7. Ver ajustes
 8. Cambiar un ajuste
 9. Ver registro de envíos y cambios
10. Importar historial de la Mac (messages.db)
11. Ver cómo conectar desde la Mac
 0. Salir
`)
		var err error
		switch ask("Opción: ") {
		case "1":
			err = cmdStatus(extra)
		case "2":
			err = cmdPair(extra)
		case "3":
			err = cmdPair(append([]string{"--phone", ask("Número con código de país (ej. +57 300 123 4567): ")}, extra...))
		case "4":
			err = cmdAllow(append(extra, "list"))
		case "5":
			who := ask("Número con código de país, o JID de grupo (…@g.us): ")
			label := ask("Nombre para reconocerlo: ")
			err = cmdAllow(append(extra, "add", who, label))
		case "6":
			err = cmdAllow(append(extra, "remove", ask("Número o JID a quitar: ")))
		case "7":
			err = cmdSettings(extra)
		case "8":
			key := ask("Ajuste (ej. sending_enabled): ")
			val := ask("Valor nuevo: ")
			err = cmdSettings(append(extra, "set", key, val))
		case "9":
			err = cmdAudit(extra)
		case "10":
			err = cmdMigrate(append([]string{"--from", strings.Trim(ask("Ruta del archivo messages.db: "), `"`)}, extra...))
		case "11":
			err = cmdConnectionInfo(extra)
		case "0", "":
			return nil
		default:
			fmt.Println("Opción no válida")
		}
		if err != nil {
			fmt.Println("Error:", err)
		}
		ask("\nPulsa Enter para seguir...")
	}
}
