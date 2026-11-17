package ebay

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// These tests need an empty Postgres database: WMCP_TEST_DB=postgres://...
func testDB(t *testing.T) *sql.DB {
	url := os.Getenv("WMCP_TEST_DB")
	if url == "" {
		t.Skip("WMCP_TEST_DB not set")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DELETE FROM ebay_sales", "DELETE FROM mail_link", "DELETE FROM app_settings WHERE key LIKE 'ebay_%'"} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { d.Close() })
	return d
}

type sent struct{ to, text string }

func TestServiceNoticeAndReminder(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	pol := policy.New(d)
	ny, _ := time.LoadLocation("America/New_York")

	if _, _, err := pol.SetSetting(ctx, "ebay_alerts_enabled", "true", policy.ActorPanel); err != nil {
		t.Fatal(err)
	}
	// Two sales in the same mailbox read, one mail sent twice, one older
	// than the moment alerts were switched on, and one that is not a sale.
	mk := func(id, subj, body string, at time.Time) Mail {
		m := Mail{ID: id, InternetID: "<" + id + "@ebay>", Subject: subj, ReceivedDateTime: at}
		m.From.EmailAddress.Address = "ebay@ebay.com"
		m.Body.Content = body
		return m
	}
	now := time.Now()
	body := func(order, qty string) string {
		return "Your buyer's shipping details:\nX Y\n1 St\nAUSTIN, TX 78701\nUnited States\nShip by: Nov 20, 2026\nOrder: " + order + "\nQuantity sold: " + qty
	}
	mails := []Mail{
		mk("old", "You made the sale for Old thing", body("10-00000-00000", "1"), now.Add(-48*time.Hour)),
		mk("a", "You made the sale for Item A", body("11-11111-11111", "1"), now.Add(time.Second)),
		mk("b", "You made the sale for Item B", body("22-22222-22222", "2"), now.Add(2*time.Second)),
		mk("b2", "You made the sale for Item B", body("22-22222-22222", "2"), now.Add(3*time.Second)),
		mk("p", "Your payout was sent", "x", now.Add(4*time.Second)),
	}
	srv, _ := fakeMS(t, mails)
	g := &Graph{TokenPath: filepath.Join(t.TempDir(), "tok.json"), AuthBase: srv.URL, APIBase: srv.URL + "/v1.0"}
	if err := g.save(&Token{ClientID: "11111111-2222-3333-4444-555555555555", Tenant: "4allfix.example", Refresh: "rt1"}); err != nil {
		t.Fatal(err)
	}
	var out []sent
	fail := false
	clock := now
	s := &Service{DB: d, Policy: pol, Graph: g, Now: func() time.Time { return clock },
		Send: func(_ context.Context, to, text string) (string, error) {
			if fail {
				return "", errors.New("not connected to WhatsApp")
			}
			out = append(out, sent{to, text})
			return "MSG" + string(rune('0'+len(out))), nil
		}}
	set, _ := pol.Settings(ctx)

	if err := s.Poll(ctx, set); err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want one grouped notice, got %d: %v", len(out), out)
	}
	n := out[0]
	if n.to != "206347995996329@lid" || !strings.HasPrefix(n.text, "Hola Ana") ||
		!strings.Contains(n.text, "- Item A, 1 unidad, para Austin, TX, enviar a más tardar el 20 de noviembre") ||
		!strings.Contains(n.text, "- Item B, 2 unidades") || strings.Contains(n.text, "Old thing") {
		t.Errorf("notice:\n%s", n.text)
	}
	// Reading the same mails again sends nothing new.
	if err := s.Poll(ctx, set); err != nil || len(out) != 1 {
		t.Fatalf("duplicate notice: %v %v", err, out)
	}
	var count int
	_ = d.QueryRow(`SELECT count(*) FROM ebay_sales`).Scan(&count)
	if count != 2 {
		t.Errorf("rows %d", count)
	}
	// The reminder is not due yet, then is due, and fails once.
	s.SendReminders(ctx, set)
	if len(out) != 1 {
		t.Fatal("reminder too early")
	}
	want := ReminderAt(now, 120, "17:00", ny)
	var due time.Time
	_ = d.QueryRow(`SELECT remind_due FROM ebay_sales WHERE mail_id = '<a@ebay>'`).Scan(&due)
	if !due.Equal(want.Truncate(time.Microsecond)) {
		t.Errorf("due %v want %v", due, want)
	}
	clock = want.Add(time.Second)
	fail = true
	s.SendReminders(ctx, set)
	fail = false
	s.SendReminders(ctx, set) // retry waits 10 minutes
	if len(out) != 1 {
		t.Fatal("retried too soon")
	}
	clock = clock.Add(11 * time.Minute)
	s.SendReminders(ctx, set)
	if len(out) != 2 || !strings.Contains(out[1].text, "te recuerdo") || !strings.Contains(out[1].text, "USPS antes de las 5:00 p.m. Gracias") ||
		!strings.Contains(out[1].text, "Item A") || !strings.Contains(out[1].text, "Item B") {
		t.Fatalf("reminder: %v", out)
	}
	s.SendReminders(ctx, set)
	if len(out) != 2 {
		t.Fatal("reminder sent twice")
	}
	st := s.Status(ctx)
	if !st.Connected || len(st.Sales) != 2 || st.Sales[0].RemindedAt == nil {
		t.Errorf("status %+v", st)
	}
	var acts int
	_ = d.QueryRow(`SELECT count(*) FROM audit_log WHERE action IN ('ebay.notice','ebay.reminder','ebay.sale')`).Scan(&acts)
	if acts < 5 {
		t.Errorf("audit rows %d", acts)
	}
}

func TestServiceOffSendsNothing(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	pol := policy.New(d)
	called := false
	s := &Service{DB: d, Policy: pol, Graph: &Graph{TokenPath: filepath.Join(t.TempDir(), "none.json")},
		Send: func(context.Context, string, string) (string, error) { called = true; return "", nil }}
	set, _ := pol.Settings(ctx)
	if set.EbayAlertsEnabled {
		t.Fatal("alerts must start off")
	}
	if err := s.Poll(ctx, set); err != nil || called {
		t.Fatalf("%v %v", err, called)
	}
}
