package ebay

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fake sale in the same shape as eBay's mail (all data made up).
const fakeHTML = `<html><head><style>.x{color:red}</style></head><body>
<table><tr><td>Hi seller,</td></tr>
<tr><td>&#847;&zwnj;Your item sold! Ship it soon.</td></tr>
<tr><td><b>Your buyer's shipping details:</b></td></tr>
<tr><td>Jane Example<br>123 Fake St<br>SPRINGFIELD, IL 62704-1234<br>United States</td></tr>
<tr><td>Ship by:</td><td>Nov 20, 2026</td></tr>
<tr><td>Order:</td><td>12-34567-89012</td></tr>
<tr><td>Quantity sold:</td><td>3</td></tr>
<tr><td>Sold:</td><td>$45.00</td></tr>
<tr><td>Buyer:</td><td>fakebuyer_99</td></tr>
</table><a href="https://www.ebay.com/x">View order</a></body></html>`

func TestParseHTML(t *testing.T) {
	s, err := ParseMail("You made the sale for Test Widget Blue 100g Pack of...", fakeHTML)
	if err != nil {
		t.Fatal(err)
	}
	if s.Item != "Test Widget Blue 100g Pack of…" {
		t.Errorf("item %q", s.Item)
	}
	if s.Quantity != 3 || s.Order != "12-34567-89012" || s.Place != "Springfield, IL" {
		t.Errorf("got %+v", s)
	}
	if s.ShipBy.IsZero() || SpanishDate(s.ShipBy) != "20 de noviembre" {
		t.Errorf("ship by %v %q", s.ShipBy, s.ShipText)
	}
	line := s.Line()
	for _, bad := range []string{"Jane", "123 Fake", "fakebuyer", "62704"} {
		if strings.Contains(line, bad) {
			t.Errorf("line leaks %q: %s", bad, line)
		}
	}
	if line != "Test Widget Blue 100g Pack of…, 3 unidades, para Springfield, IL, enviar a más tardar el 20 de noviembre" {
		t.Errorf("line %q", line)
	}
}

func TestParsePlainText(t *testing.T) {
	body := "Your buyer's shipping details:\r\nJohn Doe\r\n9 Elm Rd, Apt 2\r\nNEW YORK, NY 10001\r\nUnited States\r\nShip by: Dec 1, 2026\r\nOrder: 99-11111-22222\r\nQuantity sold: 1\r\n"
	s, err := ParseMail("You made the sale for Gadget", body)
	if err != nil {
		t.Fatal(err)
	}
	if s.Place != "New York, NY" || s.Quantity != 1 || s.Order != "99-11111-22222" || SpanishDate(s.ShipBy) != "1 de diciembre" {
		t.Errorf("got %+v", s)
	}
}

func TestParseNotSale(t *testing.T) {
	if _, err := ParseMail("Your payout is on its way", "x"); err == nil {
		t.Fatal("expected error")
	}
}

func TestItemCleaned(t *testing.T) {
	s, err := ParseMail("You made the sale for Item see www.evil.example or a@b.co now", "")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(s.Item, "evil") || strings.Contains(s.Item, "@") {
		t.Errorf("item %q", s.Item)
	}
}

func TestCompose(t *testing.T) {
	one := []Sale{{Item: "A", Quantity: 1, Place: "Miami, FL", ShipText: "21 de noviembre"}}
	got := Compose("Hola Ana:\n{ventas}\nAntes de las {hora}.", one, "17:00")
	odd := Compose("{ventas}", []Sale{{Item: "C", Quantity: 1, ShipText: "soon"}}, "17:00")
	if odd != "C, 1 unidad, enviar a más tardar: soon" {
		t.Errorf("got %q", odd)
	}
	want := "Hola Ana:\nA, 1 unidad, para Miami, FL, enviar a más tardar el 21 de noviembre\nAntes de las 5:00 p.m."
	if got != want {
		t.Errorf("got %q", got)
	}
	two := append(one, Sale{Item: "B", Quantity: 2})
	if g := Compose("{ventas}", two, "17:00"); g != "- A, 1 unidad, para Miami, FL, enviar a más tardar el 21 de noviembre\n- B, 2 unidades" {
		t.Errorf("got %q", g)
	}
}

func TestReminderAt(t *testing.T) {
	ny, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	at := func(d, h, m int) time.Time { return time.Date(2026, 11, d, h, m, 0, 0, ny) }
	cases := []struct {
		notified, want time.Time
	}{
		{at(17, 13, 30), at(17, 15, 30)}, // two hours later
		{at(17, 9, 0), at(17, 11, 0)},
		{at(17, 15, 30), at(17, 17, 0)}, // would be 17:30: goes at 5 that day
		{at(17, 16, 59), at(17, 17, 0)},
		{at(17, 18, 0), at(18, 17, 0)},  // sale after 5: next day at 5
		{at(17, 23, 30), at(18, 17, 0)}, // 1:30 at night: that day at 5
		{at(17, 1, 0), at(17, 17, 0)},   // 3:00 at night: that day at 5
	}
	for _, c := range cases {
		if got := ReminderAt(c.notified, 120, "17:00", ny); !got.Equal(c.want) {
			t.Errorf("notified %v: got %v want %v", c.notified, got, c.want)
		}
	}
}

// fakeMS is a pretend Microsoft sign-in + Graph.
func fakeMS(t *testing.T, mails []Mail) (*httptest.Server, *int) {
	polls := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/4allfix.example/oauth2/v2.0/devicecode", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		if !strings.Contains(r.Form.Get("scope"), "Mail.Read") {
			t.Errorf("scope %q", r.Form.Get("scope"))
		}
		json.NewEncoder(w).Encode(map[string]any{"device_code": "dev1", "user_code": "ABCD-EFGH", "verification_uri": "https://microsoft.com/devicelogin", "expires_in": 900, "interval": 1})
	})
	mux.HandleFunc("/4allfix.example/oauth2/v2.0/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		switch r.Form.Get("grant_type") {
		case "urn:ietf:params:oauth:grant-type:device_code":
			polls++
			if polls < 2 {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]any{"error": "authorization_pending"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at1", "refresh_token": "rt1", "expires_in": 3600})
		case "refresh_token":
			if r.Form.Get("refresh_token") == "dead" {
				w.WriteHeader(400)
				json.NewEncoder(w).Encode(map[string]any{"error": "invalid_grant"})
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "at2", "refresh_token": "rt2", "expires_in": 3600})
		}
	})
	mux.HandleFunc("/v1.0/me", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "Bearer at") {
			w.WriteHeader(401)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"mail": "Owner@4allfix.example"})
	})
	mux.HandleFunc("/v1.0/me/messages", func(w http.ResponseWriter, r *http.Request) {
		f := r.URL.Query().Get("$filter")
		if !strings.Contains(f, "from/emailAddress/address eq 'ebay@ebay.com'") || !strings.Contains(f, "receivedDateTime ge ") {
			t.Errorf("filter %q", f)
		}
		json.NewEncoder(w).Encode(map[string]any{"value": mails})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &polls
}

func TestGraphDeviceFlowAndMail(t *testing.T) {
	m := Mail{ID: "m1", InternetID: "<a@ebay>", Subject: "You made the sale for X", ReceivedDateTime: time.Now()}
	m.From.EmailAddress.Address = "ebay@ebay.com"
	m.Body.Content = "Quantity sold: 2"
	srv, _ := fakeMS(t, []Mail{m})
	g := &Graph{TokenPath: filepath.Join(t.TempDir(), "mail-token.json"), AuthBase: srv.URL, APIBase: srv.URL + "/v1.0"}
	if _, ok := g.Load(); ok {
		t.Fatal("no token yet")
	}
	ctx := context.Background()
	if _, err := g.StartDevice(ctx, "bad", "4allfix.example"); err == nil {
		t.Fatal("bad client id accepted")
	}
	cid := "11111111-2222-3333-4444-555555555555"
	dc, err := g.StartDevice(ctx, cid, "4allfix.example")
	if err != nil {
		t.Fatal(err)
	}
	if dc.UserCode != "ABCD-EFGH" {
		t.Fatal(dc.UserCode)
	}
	tok, err := g.WaitDevice(ctx, cid, "4allfix.example", dc)
	if err != nil {
		t.Fatal(err)
	}
	if tok.Account != "owner@4allfix.example" {
		t.Errorf("account %q", tok.Account)
	}
	// A new Graph reads the token back from disk and refreshes it.
	g2 := &Graph{TokenPath: g.TokenPath, AuthBase: srv.URL, APIBase: srv.URL + "/v1.0"}
	mails, err := g2.SaleMails(ctx, "", time.Now().Add(-time.Hour))
	if err != nil || len(mails) != 1 || mails[0].InternetID != "<a@ebay>" {
		t.Fatalf("mails %v %v", mails, err)
	}
	if t2, _ := g2.Load(); t2.Refresh != "rt2" {
		t.Errorf("refresh token not rotated: %q", t2.Refresh)
	}
	if _, err := g2.SaleMails(ctx, "not an address", time.Now()); err == nil {
		t.Error("bad mailbox accepted")
	}
	// A refresh token Microsoft no longer accepts asks to sign in again.
	g3 := &Graph{TokenPath: filepath.Join(t.TempDir(), "t.json"), AuthBase: srv.URL, APIBase: srv.URL + "/v1.0"}
	_ = g3.save(&Token{ClientID: cid, Tenant: "4allfix.example", Refresh: "dead"})
	if _, err := g3.Me(ctx); err != ErrSignInAgain {
		t.Errorf("got %v", err)
	}
	if err := g2.Forget(); err != nil {
		t.Fatal(err)
	}
	if _, ok := g2.Load(); ok {
		t.Error("token still there")
	}
}
