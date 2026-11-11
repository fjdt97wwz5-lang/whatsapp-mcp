package config

import "testing"

func TestValidNodeHostname(t *testing.T) {
	for h, want := range map[string]bool{
		"whatsapp-mcp": true, "wa2": true, "a": true,
		"": false, "-wa": false, "wa-": false, "WhatsApp": false, "wa mcp": false, "wa.mcp": false, "wá": false,
		"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijk":  true,
		"abcdefghijklmnopqrstuvwxyzabcdefghijklmnopqrstuvwxyzabcdefghijkl": false,
	} {
		if got := ValidNodeHostname(h); got != want {
			t.Errorf("ValidNodeHostname(%q) = %v, want %v", h, got, want)
		}
	}
}

func TestOldConfigGetsNodeHostname(t *testing.T) {
	c := &Config{}
	c.fillNew()
	if c.CloudNodeHostname != DefaultNodeHostname || c.CloudNodeEnabled {
		t.Fatalf("got %q enabled=%v", c.CloudNodeHostname, c.CloudNodeEnabled)
	}
}
