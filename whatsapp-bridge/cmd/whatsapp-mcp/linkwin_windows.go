//go:build windows

package main

import (
	"encoding/base64"
	"os"
	"os/exec"
	"syscall"
	"unicode/utf16"
)

// linkWindowScript is a small window with the one-time link and a Copiar
// button. The link comes in through the environment, not the command line.
const linkWindowScript = `
Add-Type -AssemblyName System.Windows.Forms
Add-Type -AssemblyName System.Drawing
[System.Windows.Forms.Application]::EnableVisualStyles()
$u = $env:WMCP_LINK
$f = New-Object System.Windows.Forms.Form
$f.Text = 'Panel de WhatsApp MCP'
$f.StartPosition = 'CenterScreen'
$f.FormBorderStyle = 'FixedDialog'
$f.MaximizeBox = $false
$f.MinimizeBox = $false
$f.TopMost = $true
$f.Font = New-Object System.Drawing.Font('Segoe UI', 10)
$f.ClientSize = New-Object System.Drawing.Size(580, 176)
$l = New-Object System.Windows.Forms.Label
$l.Text = "Si el navegador no se abrió, copia esta dirección y pégala en Chrome o Edge en este servidor. Vale 10 minutos y una sola vez."
$l.SetBounds(16, 12, 548, 60)
$t = New-Object System.Windows.Forms.TextBox
$t.ReadOnly = $true
$t.Text = $u
$t.SetBounds(16, 80, 548, 28)
$c = New-Object System.Windows.Forms.Button
$c.Text = 'Copiar'
$c.SetBounds(352, 124, 100, 36)
$c.Add_Click({ [System.Windows.Forms.Clipboard]::SetText($u); $c.Text = 'Copiada' })
$x = New-Object System.Windows.Forms.Button
$x.Text = 'Cerrar'
$x.SetBounds(464, 124, 100, 36)
$x.Add_Click({ $f.Close() })
$f.Controls.AddRange(@($l, $t, $c, $x))
$f.AcceptButton = $c
$f.CancelButton = $x
$f.Add_Shown({ $f.Activate(); $c.Focus() })
[void]$f.ShowDialog()
`

// showLinkWindow opens the window and returns the running process.
func showLinkWindow(u string) (*exec.Cmd, error) {
	u16 := utf16.Encode([]rune(linkWindowScript))
	b := make([]byte, 0, len(u16)*2)
	for _, c := range u16 {
		b = append(b, byte(c), byte(c>>8))
	}
	cmd := exec.Command("powershell.exe", "-NoProfile", "-STA", "-ExecutionPolicy", "Bypass",
		"-EncodedCommand", base64.StdEncoding.EncodeToString(b))
	cmd.Env = append(os.Environ(), "WMCP_LINK="+u)
	// No console of its own; the window is the form.
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return cmd, nil
}
