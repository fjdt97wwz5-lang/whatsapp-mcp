//go:build windows

package main

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

const firewallRule = "WhatsApp MCP (solo Tailscale)"

func netsh(args ...string) error {
	out, err := exec.Command(`C:\Windows\System32\netsh.exe`, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("netsh: %v %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// allowFirewall lets Tailscale addresses (and nothing else) reach the
// connector's port, and only for this program. The connector also checks
// the address and the token itself; this rule just opens the door.
func allowFirewall(exe string, port int) error {
	removeFirewall()
	return netsh("advfirewall", "firewall", "add", "rule", "name="+firewallRule,
		"dir=in", "action=allow", "protocol=TCP", "localport="+strconv.Itoa(port),
		"remoteip=100.64.0.0/10,fd7a:115c:a1e0::/48", "program="+exe, "profile=any")
}

func removeFirewall() {
	_ = netsh("advfirewall", "firewall", "delete", "rule", "name="+firewallRule)
}
