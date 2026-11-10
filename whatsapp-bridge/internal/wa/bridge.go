// Package wa runs the WhatsApp connection (whatsmeow), stores incoming
// messages, and sends messages after the policy has approved them.
package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waCompanionReg"
	"go.mau.fi/whatsmeow/store"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// Bridge owns the WhatsApp client.
type Bridge struct {
	Client   *whatsmeow.Client
	Store    *mstore.Store
	Policy   *policy.Policy
	MediaDir string
	FFmpeg   string
	Log      waLog.Logger

	container *sqlstore.Container

	mu            sync.RWMutex
	lastEvent     string
	lastChange    time.Time
	lastConnected time.Time
	pair          pairing
}

// Options configure a Bridge.
type Options struct {
	DB         *sql.DB
	Store      *mstore.Store
	Policy     *policy.Policy
	MediaDir   string
	FFmpegPath string
	DeviceName string
	Log        waLog.Logger
}

// New prepares the session store in PostgreSQL and the client. It does
// not connect to WhatsApp yet.
func New(ctx context.Context, o Options) (*Bridge, error) {
	if o.DeviceName != "" {
		store.SetOSInfo(o.DeviceName, [3]uint32{2, 0, 0})
		store.DeviceProps.PlatformType = waCompanionReg.DeviceProps_DESKTOP.Enum()
	}
	container := sqlstore.NewWithDB(o.DB, "postgres", o.Log.Sub("Session"))
	if err := container.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("session store upgrade: %w", err)
	}
	dev, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("session store: %w", err)
	}
	b := &Bridge{
		Store: o.Store, Policy: o.Policy, MediaDir: o.MediaDir, FFmpeg: o.FFmpegPath,
		Log: o.Log, container: container,
	}
	b.Client = whatsmeow.NewClient(dev, o.Log.Sub("Client"))
	b.Client.AddEventHandler(b.onEvent)
	return b, nil
}

// Paired reports whether a WhatsApp account is linked.
func (b *Bridge) Paired() bool { return b.Client.Store.ID != nil }

// Status is a short health summary.
type Status struct {
	Paired        bool       `json:"paired"`
	Connected     bool       `json:"connected"`
	LoggedIn      bool       `json:"logged_in"`
	Account       string     `json:"account,omitempty"` // linked phone number (digits)
	Name          string     `json:"name,omitempty"`    // WhatsApp profile name
	LastEvent     string     `json:"last_event,omitempty"`
	Since         time.Time  `json:"since,omitempty"`
	LastConnected *time.Time `json:"last_connected,omitempty"`
}

// Status returns the connection state.
func (b *Bridge) Status() Status {
	b.mu.RLock()
	defer b.mu.RUnlock()
	s := Status{Paired: b.Paired(), Connected: b.Client.IsConnected(), LoggedIn: b.Client.IsLoggedIn(), LastEvent: b.lastEvent, Since: b.lastChange}
	if b.Client.Store.ID != nil {
		s.Account = b.Client.Store.ID.User
		s.Name = b.Client.Store.PushName
	}
	if !b.lastConnected.IsZero() {
		t := b.lastConnected
		s.LastConnected = &t
	}
	return s
}

func (b *Bridge) setEvent(e string) {
	b.mu.Lock()
	b.lastEvent, b.lastChange = e, time.Now()
	b.mu.Unlock()
}

// Start connects if an account is linked. Without one it does nothing and
// the connector keeps serving the stored history (read only).
func (b *Bridge) Start() error {
	if !b.Paired() {
		b.setEvent("not linked: run 'whatsapp-mcp pair' on the server")
		b.Log.Warnf("No WhatsApp account linked yet. Run the 'Vincular WhatsApp' shortcut on the server.")
		return nil
	}
	b.setEvent("connecting")
	return b.Client.Connect() // whatsmeow reconnects automatically afterwards
}

// Stop disconnects.
func (b *Bridge) Stop() { b.Client.Disconnect() }

// ErrAlreadyPaired is returned by Pair when a session exists.
var ErrAlreadyPaired = errors.New("a WhatsApp account is already linked; use 'whatsapp-mcp unlink' first")

// pairLoop links a WhatsApp account. With phone == "" it reports each QR
// code through onQR; otherwise it asks WhatsApp for an 8 character code
// (typed in WhatsApp > Linked devices > Link with phone number) and
// reports it through onCode. It returns when linked, on error or timeout.
func (b *Bridge) pairLoop(ctx context.Context, phone string, onQR func(code string), onCode func(code string)) error {
	if b.Paired() {
		return ErrAlreadyPaired
	}
	if b.Client.IsConnected() {
		b.Client.Disconnect()
	}
	qrChan, err := b.Client.GetQRChannel(ctx)
	if err != nil {
		return err
	}
	if err := b.Client.Connect(); err != nil {
		return err
	}
	if phone != "" {
		// Give the socket a moment, then request a code.
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			b.Client.Disconnect()
			return ctx.Err()
		}
		code, err := b.Client.PairPhone(ctx, mstore.DigitsOnly(phone), true, whatsmeow.PairClientChrome, "Chrome (Windows)")
		if err != nil {
			b.Client.Disconnect()
			return fmt.Errorf("pairing code: %w", err)
		}
		onCode(code)
	}
	for evt := range qrChan {
		switch evt.Event {
		case "code":
			if phone == "" {
				onQR(evt.Code)
			}
		case "success":
			b.setEvent("linked")
			return nil
		case "timeout":
			b.Client.Disconnect()
			return ErrPairTimeout
		default:
			if evt.Error != nil {
				b.Client.Disconnect()
				return evt.Error
			}
		}
	}
	b.Client.Disconnect()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return errors.New("pairing ended without success")
}

// ErrPairTimeout means the code was not used in time.
var ErrPairTimeout = errors.New("time ran out before the code was used; run it again")

// Pair links a WhatsApp account from the console: it prints the QR (and
// opens it in the browser, where it always fits) or the 8 character code.
func (b *Bridge) Pair(ctx context.Context, out io.Writer, phone string) error {
	page := newQRPage()
	defer page.finish(false, "La vinculación se canceló. Vuelve a abrir \"Vincular WhatsApp\".")
	err := b.pairLoop(ctx, phone, func(code string) {
		if err := page.showCode(code); err != nil {
			fmt.Fprintln(out, "(No se pudo abrir el QR en el navegador:", err, ")")
		} else {
			fmt.Fprintln(out, "\nSe abrió el código QR en tu navegador (página \"Vincular WhatsApp\").")
		}
		fmt.Fprintln(out, "Escanéalo con WhatsApp > Dispositivos vinculados > Vincular un dispositivo.")
		fmt.Fprintln(out, "Si el navegador no se abrió, usa este (agranda la ventana si sale cortado):")
		qrterminal.GenerateWithConfig(code, qrterminal.Config{Level: qrterminal.L, Writer: out, HalfBlocks: true,
			BlackChar: qrterminal.BLACK_BLACK, WhiteChar: qrterminal.WHITE_WHITE, BlackWhiteChar: qrterminal.BLACK_WHITE,
			WhiteBlackChar: qrterminal.WHITE_BLACK, QuietZone: 1})
		fmt.Fprintln(out, "También puedes usar un código de 8 letras: opción 3 del menú.")
	}, func(code string) {
		fmt.Fprintf(out, "\nEn tu teléfono: WhatsApp > Dispositivos vinculados > Vincular con número de teléfono\nCódigo: %s\n\n", code)
	})
	switch {
	case err == nil:
		page.finish(true, "Listo. Ya puedes cerrar esta página.")
		fmt.Fprintln(out, "\nWhatsApp vinculado.")
	case errors.Is(err, ErrPairTimeout):
		page.finish(false, "El tiempo se acabó antes de escanear. Vuelve a abrir \"Vincular WhatsApp\".")
	default:
		page.finish(false, "Hubo un error: "+err.Error())
	}
	return err
}

// Unlink logs out and deletes the session.
func (b *Bridge) Unlink(ctx context.Context) error {
	if !b.Paired() {
		return nil
	}
	if b.Client.IsConnected() || b.Client.Connect() == nil {
		if err := b.Client.Logout(ctx); err == nil {
			return nil
		}
	}
	return b.Client.Store.Delete(ctx)
}

func (b *Bridge) onEvent(evt any) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	switch v := evt.(type) {
	case *events.Message:
		b.handleMessage(ctx, v)
	case *events.HistorySync:
		b.handleHistorySync(ctx, v)
	case *events.Connected:
		b.setEvent("connected")
		b.mu.Lock()
		b.lastConnected = time.Now()
		b.mu.Unlock()
		b.Log.Infof("Connected to WhatsApp")
	case *events.Disconnected:
		b.setEvent("disconnected (reconnecting)")
	case *events.LoggedOut:
		b.setEvent("logged out: link the account again")
		b.Log.Warnf("WhatsApp logged this device out; run 'Vincular WhatsApp' again")
		b.Policy.Audit(ctx, policy.ActorSystem, "session.logged_out", "", "ok", map[string]any{"reason": v.Reason.String()})
		// Get a fresh device so it can be linked again from the panel
		// without restarting the service (not from inside this handler).
		go func() {
			time.Sleep(2 * time.Second)
			if err := b.resetDevice(context.Background()); err != nil {
				b.Log.Warnf("could not prepare a new device after logout: %v", err)
			}
		}()
	case *events.StreamReplaced:
		b.setEvent("stream replaced: another client with the same session connected")
		b.Log.Errorf("Another client is using this session (is the old bridge still running on the Mac?)")
	case *events.TemporaryBan:
		b.setEvent("temporary ban: " + v.String())
		b.Log.Errorf("WhatsApp temporary ban: %s", v.String())
		b.Policy.Audit(ctx, policy.ActorSystem, "session.temporary_ban", "", "warning", map[string]any{"detail": v.String()})
	}
}
