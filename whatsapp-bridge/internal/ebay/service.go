package ebay

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
	_ "time/tzdata" // Windows has no zone database of its own

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// Sender sends a WhatsApp text through the connector (approved list, rate
// limit and audit included) and returns the message ID.
type Sender func(ctx context.Context, recipient, text string) (string, error)

// Logger is the small logging interface used here.
type Logger interface {
	Infof(string, ...any)
	Warnf(string, ...any)
}

// Service polls the mailbox and sends the notices and reminders.
type Service struct {
	DB     *sql.DB
	Policy *policy.Policy
	Graph  *Graph
	Send   Sender
	Log    Logger
	Now    func() time.Time

	mu      sync.Mutex
	device  *DeviceCode
	devErr  string
	devStop context.CancelFunc
	kick    chan struct{}
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) warnf(f string, a ...any) {
	if s.Log != nil {
		s.Log.Warnf(f, a...)
	}
}

// SaleRow is one sale as the panel shows it.
type SaleRow struct {
	MailID      string     `json:"-"`
	ReceivedAt  time.Time  `json:"received_at"`
	Item        string     `json:"item"`
	Quantity    int        `json:"quantity"`
	Place       string     `json:"place"`
	ShipBy      string     `json:"ship_by"`
	Order       string     `json:"order"`
	NotifiedAt  *time.Time `json:"notified_at,omitempty"`
	NotifyError string     `json:"notify_error,omitempty"`
	RemindDue   *time.Time `json:"remind_due,omitempty"`
	RemindedAt  *time.Time `json:"reminded_at,omitempty"`
	RemindError string     `json:"remind_error,omitempty"`
}

// Status is what the panel shows.
type Status struct {
	Connected   bool        `json:"connected"`
	Account     string      `json:"account,omitempty"`
	ClientID    string      `json:"client_id,omitempty"`
	Tenant      string      `json:"tenant,omitempty"`
	Device      *DeviceCode `json:"device,omitempty"`
	DeviceError string      `json:"device_error,omitempty"`
	LastPoll    *time.Time  `json:"last_poll,omitempty"`
	LastError   string      `json:"last_error,omitempty"`
	WatchSince  *time.Time  `json:"watch_since,omitempty"`
	Sales       []SaleRow   `json:"sales"`
}

// Status returns the state for the panel.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Sales: []SaleRow{}}
	if t, ok := s.Graph.Load(); ok {
		st.Connected, st.Account, st.ClientID, st.Tenant = true, t.Account, t.ClientID, t.Tenant
	}
	var lastPoll, since sql.NullTime
	var cid, ten string
	_ = s.DB.QueryRowContext(ctx, `SELECT client_id, tenant, last_poll_at, watch_since, last_error FROM mail_link WHERE id = 1`).
		Scan(&cid, &ten, &lastPoll, &since, &st.LastError)
	if !st.Connected {
		st.ClientID, st.Tenant = cid, ten // keep what was typed last time
	}
	if lastPoll.Valid {
		st.LastPoll = &lastPoll.Time
	}
	if since.Valid {
		st.WatchSince = &since.Time
	}
	s.mu.Lock()
	if s.device != nil && s.now().Before(s.device.Expires) {
		d := *s.device
		st.Device = &d
	}
	st.DeviceError = s.devErr
	s.mu.Unlock()
	rows, err := s.DB.QueryContext(ctx, `SELECT mail_id, received_at, item, quantity, buyer_place, ship_by, order_number,
		notified_at, notify_error, remind_due, reminded_at, remind_error FROM ebay_sales ORDER BY received_at DESC LIMIT 30`)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var r SaleRow
			if rows.Scan(&r.MailID, &r.ReceivedAt, &r.Item, &r.Quantity, &r.Place, &r.ShipBy, &r.Order,
				&r.NotifiedAt, &r.NotifyError, &r.RemindDue, &r.RemindedAt, &r.RemindError) == nil {
				st.Sales = append(st.Sales, r)
			}
		}
	}
	return st
}

// Connect starts the Microsoft sign-in. The panel then shows the code.
func (s *Service) Connect(ctx context.Context, clientID, tenant string) (*DeviceCode, error) {
	clientID, tenant = strings.TrimSpace(clientID), strings.ToLower(strings.TrimSpace(tenant))
	if tenant == "" {
		tenant = "organizations"
	}
	dc, err := s.Graph.StartDevice(ctx, clientID, tenant)
	if err != nil {
		return nil, err
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO mail_link (id, client_id, tenant, updated_at) VALUES (1, $1, $2, now())
		ON CONFLICT (id) DO UPDATE SET client_id = EXCLUDED.client_id, tenant = EXCLUDED.tenant, updated_at = now()`, clientID, tenant)
	wctx, cancel := context.WithTimeout(context.Background(), time.Until(dc.Expires)+time.Minute)
	s.mu.Lock()
	if s.devStop != nil {
		s.devStop()
	}
	s.device, s.devErr, s.devStop = dc, "", cancel
	s.mu.Unlock()
	go func() {
		defer cancel()
		t, err := s.Graph.WaitDevice(wctx, clientID, tenant, dc)
		s.mu.Lock()
		if s.device == dc {
			s.device = nil
			if err != nil {
				s.devErr = err.Error()
			}
		}
		s.mu.Unlock()
		bg := context.Background()
		if err != nil {
			if !errors.Is(err, context.Canceled) {
				s.Policy.Audit(bg, policy.ActorPanel, "ebay.connect", "", "error", map[string]any{"error": err.Error()})
			}
			return
		}
		_, _ = s.DB.ExecContext(bg, `UPDATE mail_link SET account = $1, connected_at = now(), last_error = '', updated_at = now() WHERE id = 1`, t.Account)
		s.Policy.Audit(bg, policy.ActorPanel, "ebay.connect", t.Account, "ok", map[string]any{"tenant": tenant})
		s.CheckNow()
	}()
	return dc, nil
}

// Disconnect forgets the Microsoft sign-in.
func (s *Service) Disconnect(ctx context.Context) error {
	s.mu.Lock()
	if s.devStop != nil {
		s.devStop()
	}
	s.device, s.devErr = nil, ""
	s.mu.Unlock()
	if err := s.Graph.Forget(); err != nil {
		return err
	}
	_, err := s.DB.ExecContext(ctx, `UPDATE mail_link SET account = '', connected_at = NULL, updated_at = now() WHERE id = 1`)
	return err
}

// CheckNow makes the loop look at the mailbox right away.
func (s *Service) CheckNow() {
	s.mu.Lock()
	k := s.kick
	s.mu.Unlock()
	if k != nil {
		select {
		case k <- struct{}{}:
		default:
		}
	}
}

// Run polls the mailbox every ebay_poll_minutes and sends reminders when
// they are due. It does nothing while ebay_alerts_enabled is off.
func (s *Service) Run(ctx context.Context) {
	s.mu.Lock()
	s.kick = make(chan struct{}, 1)
	k := s.kick
	s.mu.Unlock()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	var lastPoll time.Time
	for {
		force := false
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		case <-k:
			force = true
		}
		set, err := s.Policy.Settings(ctx)
		if err != nil || !set.EbayAlertsEnabled {
			continue
		}
		every := time.Duration(max(set.EbayPollMinutes, 1)) * time.Minute
		if force || s.now().Sub(lastPoll) >= every {
			lastPoll = s.now()
			if err := s.Poll(ctx, set); err != nil {
				s.warnf("eBay: %v", err)
			}
		}
		s.SendReminders(ctx, set)
	}
}

// watchSince is the earliest mail that may cause a notice: the moment the
// alerts were last switched on (never older than a day), so turning them on
// never sends a flood of old sales.
func (s *Service) watchSince(ctx context.Context) time.Time {
	floor := s.now().Add(-24 * time.Hour)
	var on sql.NullTime
	_ = s.DB.QueryRowContext(ctx, `SELECT updated_at FROM app_settings WHERE key = 'ebay_alerts_enabled'`).Scan(&on)
	since := floor
	if on.Valid && on.Time.After(since) {
		since = on.Time
	}
	_, _ = s.DB.ExecContext(ctx, `INSERT INTO mail_link (id, watch_since) VALUES (1, $1)
		ON CONFLICT (id) DO UPDATE SET watch_since = EXCLUDED.watch_since`, since)
	return since
}

// Poll reads new sale mails, stores them and sends the notice.
func (s *Service) Poll(ctx context.Context, set policy.Settings) error {
	if _, ok := s.Graph.Load(); !ok {
		return nil
	}
	since := s.watchSince(ctx)
	mails, err := s.Graph.SaleMails(ctx, set.EbayMailbox, since.Add(-time.Minute))
	errText := ""
	if err != nil {
		errText = err.Error()
	}
	var prev string
	_ = s.DB.QueryRowContext(ctx, `SELECT last_error FROM mail_link WHERE id = 1`).Scan(&prev)
	_, _ = s.DB.ExecContext(ctx, `UPDATE mail_link SET last_poll_at = now(), last_error = $1 WHERE id = 1`, errText)
	if err != nil {
		if errText != prev { // log each new problem once, not every poll
			s.Policy.Audit(ctx, policy.ActorEbay, "ebay.poll_error", "", "error", map[string]any{"error": errText})
		}
		return err
	}
	for _, m := range mails {
		if !strings.EqualFold(m.From.EmailAddress.Address, SaleSender) || m.ReceivedDateTime.Before(since) {
			continue
		}
		sale, err := ParseMail(m.Subject, m.Body.Content)
		if err != nil {
			continue // another kind of eBay mail
		}
		id := m.InternetID
		if id == "" {
			id = m.ID
		}
		if sale.Order != "" {
			var n int
			_ = s.DB.QueryRowContext(ctx, `SELECT count(*) FROM ebay_sales WHERE order_number = $1 AND item = $2`, sale.Order, sale.Item).Scan(&n)
			if n > 0 {
				continue // same sale, mail sent twice
			}
		}
		ship := sale.ShipText
		if !sale.ShipBy.IsZero() {
			ship = SpanishDate(sale.ShipBy)
		}
		r, err := s.DB.ExecContext(ctx, `INSERT INTO ebay_sales (mail_id, received_at, subject, item, quantity, buyer_place, ship_by, order_number)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8) ON CONFLICT (mail_id) DO NOTHING`,
			id, m.ReceivedDateTime, safeText(m.Subject, 200), sale.Item, sale.Quantity, sale.Place, ship, sale.Order)
		if err != nil {
			return err
		}
		if n, _ := r.RowsAffected(); n > 0 {
			s.Policy.Audit(ctx, policy.ActorEbay, "ebay.sale", sale.Order, "ok", map[string]any{"item": sale.Item, "quantity": sale.Quantity, "place": sale.Place})
		}
	}
	s.sendNotices(ctx, set)
	return nil
}

func (s *Service) rows(ctx context.Context, where string, args ...any) ([]SaleRow, error) {
	return s.query(ctx, where+` ORDER BY received_at`, args...)
}

func (s *Service) query(ctx context.Context, tail string, args ...any) ([]SaleRow, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT mail_id, received_at, item, quantity, buyer_place, ship_by, order_number FROM ebay_sales WHERE `+tail, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SaleRow
	for rows.Next() {
		var r SaleRow
		if err := rows.Scan(&r.MailID, &r.ReceivedAt, &r.Item, &r.Quantity, &r.Place, &r.ShipBy, &r.Order); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func toSales(rs []SaleRow) []Sale {
	var out []Sale
	for _, r := range rs {
		out = append(out, Sale{Item: r.Item, Quantity: r.Quantity, Place: r.Place, ShipText: r.ShipBy})
	}
	return out
}

func ids(rs []SaleRow) []string {
	var out []string
	for _, r := range rs {
		out = append(out, r.MailID)
	}
	return out
}

// sendNotices sends one message with every sale not notified yet. A failed
// send is tried again on the next poll for up to 12 hours.
func (s *Service) sendNotices(ctx context.Context, set policy.Settings) {
	pend, err := s.rows(ctx, `notified_at IS NULL AND created_at > now() - interval '12 hours'`)
	if err != nil || len(pend) == 0 {
		return
	}
	text := Compose(set.EbayNoticeText, toSales(pend), set.EbayCutoffTime)
	msgID, err := s.Send(ctx, set.EbayAlertChat, text)
	if err != nil {
		_, _ = s.DB.ExecContext(ctx, `UPDATE ebay_sales SET notify_error = $1 WHERE mail_id = ANY($2)`, err.Error(), ids(pend))
		s.Policy.Audit(ctx, policy.ActorEbay, "ebay.notice", set.EbayAlertChat, "error", map[string]any{"sales": len(pend), "error": err.Error()})
		return
	}
	s.Policy.Audit(ctx, policy.ActorEbay, "ebay.notice", set.EbayAlertChat, "ok", map[string]any{"sales": len(pend), "message_id": msgID})
	now := s.now()
	var due any
	if set.EbayReminderMinutes > 0 {
		loc, lerr := time.LoadLocation(set.EbayTimezone)
		if lerr != nil {
			loc, _ = time.LoadLocation("America/New_York")
		}
		due = ReminderAt(now, set.EbayReminderMinutes, set.EbayCutoffTime, loc)
	}
	_, _ = s.DB.ExecContext(ctx, `UPDATE ebay_sales SET notified_at = $1, notify_msg_id = $2, notify_error = '', remind_due = $3, recipient = $4
		WHERE mail_id = ANY($5)`, now, msgID, due, set.EbayAlertChat, ids(pend))
}

// SendReminders sends one message with every reminder that is due.
func (s *Service) SendReminders(ctx context.Context, set policy.Settings) {
	due, err := s.rows(ctx, `notified_at IS NOT NULL AND reminded_at IS NULL AND remind_due IS NOT NULL AND remind_due <= $1`, s.now())
	if err != nil || len(due) == 0 {
		return
	}
	text := Compose(set.EbayReminderText, toSales(due), set.EbayCutoffTime)
	msgID, err := s.Send(ctx, set.EbayAlertChat, text)
	if err != nil {
		// Try again in 10 minutes; give up after a day.
		_, _ = s.DB.ExecContext(ctx, `UPDATE ebay_sales SET remind_error = $1,
			remind_due = CASE WHEN notified_at < now() - interval '1 day' THEN NULL ELSE $2::timestamptz END
			WHERE mail_id = ANY($3)`, err.Error(), s.now().Add(10*time.Minute), ids(due))
		s.Policy.Audit(ctx, policy.ActorEbay, "ebay.reminder", set.EbayAlertChat, "error", map[string]any{"sales": len(due), "error": err.Error()})
		return
	}
	s.Policy.Audit(ctx, policy.ActorEbay, "ebay.reminder", set.EbayAlertChat, "ok", map[string]any{"sales": len(due), "message_id": msgID})
	_, _ = s.DB.ExecContext(ctx, `UPDATE ebay_sales SET reminded_at = $1, remind_msg_id = $2, remind_error = '' WHERE mail_id = ANY($3)`,
		s.now(), msgID, ids(due))
}

// Preview shows the notice and reminder texts for the newest sale (or an
// example), without sending anything.
func (s *Service) Preview(ctx context.Context) (notice, reminder string, err error) {
	set, err := s.Policy.Settings(ctx)
	if err != nil {
		return "", "", err
	}
	sales := []Sale{{Item: "Producto de ejemplo", Quantity: 1, Place: "Chicago, IL", ShipText: SpanishDate(s.now().AddDate(0, 0, 1))}}
	if rs, err := s.query(ctx, `true ORDER BY received_at DESC LIMIT 1`); err == nil && len(rs) > 0 {
		sales = toSales(rs)
	}
	return Compose(set.EbayNoticeText, sales, set.EbayCutoffTime), Compose(set.EbayReminderText, sales, set.EbayCutoffTime), nil
}

// Describe is a one-line state for logs.
func (s Status) Describe() string {
	if !s.Connected {
		return "not connected"
	}
	return fmt.Sprintf("connected as %s", s.Account)
}
