package wa

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
)

// PairState is what the web panel shows while linking.
type PairState struct {
	State   string    `json:"state"` // idle, starting, qr, code, linked, error, timeout, cancelled
	Mode    string    `json:"mode,omitempty"`
	QR      string    `json:"-"`              // raw QR text (the panel turns it into an image)
	Code    string    `json:"code,omitempty"` // 8 character pairing code
	Error   string    `json:"error,omitempty"`
	Updated time.Time `json:"updated"`
}

type pairing struct {
	mu     sync.Mutex
	state  PairState
	cancel context.CancelFunc
}

func (p *pairing) set(f func(*PairState)) {
	p.mu.Lock()
	f(&p.state)
	p.state.Updated = time.Now()
	p.mu.Unlock()
}

// PairStatus returns the current linking state.
func (b *Bridge) PairStatus() PairState {
	b.pair.mu.Lock()
	defer b.pair.mu.Unlock()
	return b.pair.state
}

// ErrPairRunning is returned when a linking is already in progress.
var ErrPairRunning = errors.New("ya hay una vinculación en curso")

// StartPairing links an account in the background (used by the web panel
// inside the running service). phone == "" means QR.
func (b *Bridge) StartPairing(phone, actor string) error {
	if b.Paired() {
		return ErrAlreadyPaired
	}
	b.pair.mu.Lock()
	if b.pair.cancel != nil {
		b.pair.mu.Unlock()
		return ErrPairRunning
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	b.pair.cancel = cancel
	mode := "qr"
	if phone != "" {
		mode = "phone"
	}
	b.pair.state = PairState{State: "starting", Mode: mode, Updated: time.Now()}
	b.pair.mu.Unlock()

	go func() {
		defer func() {
			b.pair.mu.Lock()
			b.pair.cancel = nil
			b.pair.mu.Unlock()
			cancel()
		}()
		err := b.pairLoop(ctx, phone,
			func(code string) { b.pair.set(func(s *PairState) { s.State, s.QR = "qr", code }) },
			func(code string) { b.pair.set(func(s *PairState) { s.State, s.Code = "code", code }) })
		actx, acancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer acancel()
		switch {
		case err == nil:
			b.pair.set(func(s *PairState) { s.State, s.QR, s.Code = "linked", "", "" })
			b.Policy.Audit(actx, actor, "session.paired", b.Status().Account, "ok", map[string]any{"mode": mode})
		case errors.Is(err, context.Canceled):
			b.pair.set(func(s *PairState) { s.State, s.QR, s.Code = "cancelled", "", "" })
		case errors.Is(err, ErrPairTimeout), errors.Is(err, context.DeadlineExceeded):
			b.pair.set(func(s *PairState) { s.State, s.QR, s.Code = "timeout", "", "" })
		default:
			b.pair.set(func(s *PairState) { s.State, s.QR, s.Code, s.Error = "error", "", "", err.Error() })
			b.Policy.Audit(actx, actor, "session.paired", "", "error", map[string]any{"mode": mode, "error": err.Error()})
		}
	}()
	return nil
}

// CancelPairing stops a linking in progress.
func (b *Bridge) CancelPairing() {
	b.pair.mu.Lock()
	c := b.pair.cancel
	b.pair.mu.Unlock()
	if c != nil {
		c()
	}
}

// Resolve gives the canonical JID for a number or JID (the phone-number
// form when WhatsApp told us the mapping) and every alias the approved list
// may hold for it.
func (b *Bridge) Resolve(ctx context.Context, recipient string) (string, []string, error) {
	jid, aliases, err := b.resolve(ctx, recipient)
	if err != nil {
		return "", nil, err
	}
	return jid.ToNonAD().String(), aliases, nil
}

// resetDevice gives the client a fresh, empty device after the account was
// unlinked, so it can be linked again without restarting the service.
func (b *Bridge) resetDevice(ctx context.Context) error {
	dev := b.container.NewDevice()
	old := b.Client
	old.RemoveEventHandlers()
	old.Disconnect()
	c := whatsmeow.NewClient(dev, b.Log.Sub("Client"))
	c.AddEventHandler(b.onEvent)
	b.mu.Lock()
	b.Client = c
	b.mu.Unlock()
	b.setEvent("not linked")
	return nil
}

// UnlinkHere logs the account out (it disappears from Linked devices on the
// phone) and leaves the service ready to link another one.
func (b *Bridge) UnlinkHere(ctx context.Context) error {
	if err := b.Unlink(ctx); err != nil {
		return err
	}
	return b.resetDevice(ctx)
}
