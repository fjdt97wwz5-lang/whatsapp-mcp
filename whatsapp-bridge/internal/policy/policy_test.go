package policy

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
)

func TestNormalizeRecipient(t *testing.T) {
	ok := map[string]string{
		"+57 300 123 4567":               "573001234567@s.whatsapp.net",
		"(305) 555-1234 ":                "3055551234@s.whatsapp.net",
		"573001234567@s.whatsapp.net":    "573001234567@s.whatsapp.net",
		"573001234567:12@s.whatsapp.net": "573001234567@s.whatsapp.net",
		"120363012345678901@g.us":        "120363012345678901@g.us",
		"123456789012345@lid":            "123456789012345@lid",
	}
	for in, want := range ok {
		got, _, err := NormalizeRecipient(in)
		if err != nil || got != want {
			t.Errorf("NormalizeRecipient(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	for _, in := range []string{"", "hola", "123", "abc@s.whatsapp.net", "x@evil.com", "@g.us", "1234567890123456789"} {
		if got, _, err := NormalizeRecipient(in); err == nil {
			t.Errorf("NormalizeRecipient(%q) = %q, want error", in, got)
		}
	}
}

func TestWithin(t *testing.T) {
	base := filepath.FromSlash("/data/enviar")
	cases := map[string]bool{
		"/data/enviar/a.pdf":        true,
		"/data/enviar/sub/b.pdf":    true,
		"/data/enviar":              false,
		"/data/enviar2/a.pdf":       false,
		"/data/enviar/../secret":    false,
		"/etc/passwd":               false,
		"/data/enviar/..hidden.pdf": true,
	}
	for p, want := range cases {
		if got := Within(base, filepath.Clean(filepath.FromSlash(p))); got != want {
			t.Errorf("Within(%q) = %v want %v", p, got, want)
		}
	}
}

func TestParseValue(t *testing.T) {
	d, _ := DefByKey("max_sends_per_minute")
	if _, err := ParseValue(d, "0"); err == nil {
		t.Error("expected range error")
	}
	if v, err := ParseValue(d, "15"); err != nil || v.(int) != 15 {
		t.Error("expected 15")
	}
	b, _ := DefByKey("sending_enabled")
	if v, _ := ParseValue(b, "Sí"); v != true {
		t.Error("sí should be true")
	}
}

// Integration test against a real PostgreSQL (set WHATSAPP_MCP_TEST_DB).
func TestPolicyWithDB(t *testing.T) {
	url := os.Getenv("WHATSAPP_MCP_TEST_DB")
	if url == "" {
		t.Skip("WHATSAPP_MCP_TEST_DB not set")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	p := New(d)
	if prev, err := p.Settings(ctx); err == nil {
		t.Cleanup(func() {
			p.SetSetting(context.Background(), "attachments_folder", prev.AttachmentsFolder, ActorManual)
		})
	}
	dir := t.TempDir()
	allowed := filepath.Join(dir, "enviar")
	os.MkdirAll(allowed, 0o700)
	inside := filepath.Join(allowed, "ok.txt")
	outside := filepath.Join(dir, "secret.txt")
	os.WriteFile(inside, []byte("x"), 0o600)
	os.WriteFile(outside, []byte("y"), 0o600)
	link := filepath.Join(allowed, "link.txt")
	_ = os.Symlink(outside, link)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	_, _, err = p.SetSetting(ctx, "attachments_folder", allowed, ActorManual)
	must(err)
	_, _, err = p.SetSetting(ctx, "attachments_folder_only", "false", ActorManual)
	must(err)
	if _, err := p.CheckAttachment(ctx, outside); err != nil {
		t.Errorf("folder rule off: outside file should pass: %v", err)
	}
	_, _, err = p.SetSetting(ctx, "attachments_folder_only", "true", ActorMCP)
	must(err)
	if _, err := p.CheckAttachment(ctx, inside); err != nil {
		t.Errorf("inside file should pass: %v", err)
	}
	if _, err := p.CheckAttachment(ctx, outside); err == nil {
		t.Error("outside file should be refused")
	}
	if _, err := p.CheckAttachment(ctx, link); err == nil {
		t.Error("symlink escaping the folder should be refused")
	}
	if _, err := p.CheckAttachment(ctx, allowed); err == nil {
		t.Error("a folder is not a file")
	}
	_, _, err = p.SetSetting(ctx, "attachments_folder_only", "false", ActorManual)
	must(err)

	// Locked settings cannot be changed from MCP.
	if _, _, err := p.SetSetting(ctx, "send_only_to_approved", "false", ActorMCP); err == nil {
		t.Error("send_only_to_approved must be locked from MCP")
	}
	// With MCP changes switched off by hand, Claude cannot edit the list.
	_, _, err = p.SetSetting(ctx, "mcp_can_change_settings", "false", ActorManual)
	must(err)
	if _, err := p.AddApproved(ctx, "+1 555 123 4567", "x", ActorMCP); err == nil {
		t.Error("MCP add should be refused when mcp_can_change_settings=false")
	}
	_, _, err = p.SetSetting(ctx, "mcp_can_change_settings", "true", ActorManual)
	must(err)

	// Approved list + aliases.
	_, err = p.AddApproved(ctx, "+1 555 123 4567", "test", ActorMCP)
	must(err)
	if err := p.CheckSend(ctx, []string{"15551234567@s.whatsapp.net"}); err != nil {
		t.Errorf("approved contact refused: %v", err)
	}
	if err := p.CheckSend(ctx, []string{"999@lid", "15551234567@s.whatsapp.net"}); err != nil {
		t.Errorf("approved contact via alias refused: %v", err)
	}
	if err := p.CheckSend(ctx, []string{"15550000001@s.whatsapp.net"}); err == nil {
		t.Error("unapproved contact allowed")
	}
	_, err = p.RemoveApproved(ctx, "15551234567", ActorManual)
	must(err)

	// Rate limit.
	_, _, err = p.SetSetting(ctx, "max_sends_per_minute", "2", ActorManual)
	must(err)
	p.sends = nil
	if p.TakeSendSlot(ctx) != nil || p.TakeSendSlot(ctx) != nil {
		t.Error("first two sends should pass")
	}
	if p.TakeSendSlot(ctx) == nil {
		t.Error("third send in a minute should be refused")
	}
	_, _, err = p.SetSetting(ctx, "max_sends_per_minute", "10", ActorManual)
	must(err)
}
