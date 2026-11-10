// Package panel is the web panel: status, linking WhatsApp, conversations,
// the approved list, settings and the log, in Spanish, in the browser.
//
// Access: a one-time link made on the server by "whatsapp-mcp panel" (only
// administrators can make one), or the access key on the login page. From
// other Tailscale devices only when panel_remote is on. Never via Funnel:
// the cloud listener does not serve the panel at all.
package panel

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	waLog "go.mau.fi/whatsmeow/util/log"
	"rsc.io/qr"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/sqlitemigrate"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/version"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

//go:embed static
var staticFS embed.FS

// Deps are what the panel works with.
type Deps struct {
	Cfg          *config.Config
	Store        *mstore.Store
	Policy       *policy.Policy
	Bridge       *wa.Bridge
	Log          waLog.Logger
	TailscaleIPs func() []net.IP
	Cloud        CloudControl // nil when there is no cloud access
}

// CloudControl is what the panel can see and do about the cloud access.
type CloudControl interface {
	Info(ctx context.Context) CloudInfo
	SetFunnel(ctx context.Context, on bool) error
	RotateKey() (string, error)
}

// CloudInfo describes the Funnel access for the panel.
type CloudInfo struct {
	Enabled     bool   `json:"enabled"`
	Listening   bool   `json:"listening"`
	LocalAddr   string `json:"local_addr"`
	URL         string `json:"url,omitempty"` // public connector address (holds the cloud key)
	FunnelCmd   string `json:"funnel_cmd"`
	Funnel      bool   `json:"funnel"`       // Funnel publishes the local port now
	FunnelKnown bool   `json:"funnel_known"` // false when tailscale could not be asked
	Note        string `json:"note,omitempty"`
}

// Panel serves /panel/.
type Panel struct {
	d        Deps
	sessions sessions
	limit    limiter
	static   http.Handler
}

// New makes the panel handler.
func New(d Deps) *Panel {
	sub, _ := fs.Sub(staticFS, "static")
	return &Panel{d: d, sessions: sessions{m: map[string]*session{}}, limit: limiter{fails: map[string][]time.Time{}},
		static: http.StripPrefix("/panel/", http.FileServer(http.FS(sub)))}
}

// Allowed says whether this caller may reach the panel at all: the server
// itself always, other Tailscale devices only with panel_remote on.
func (p *Panel) Allowed(r *http.Request) bool {
	ip := clientIP(r)
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	s, err := p.d.Policy.Settings(r.Context())
	return err == nil && s.PanelRemote
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]any{"ok": false, "error": msg})
}

func securityHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self'; script-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("X-Frame-Options", "DENY")
	h.Set("Referrer-Policy", "no-referrer")
}

// ServeHTTP routes /panel/...
func (p *Panel) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	securityHeaders(w)
	if !p.Allowed(r) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte("El panel solo se puede abrir en el propio servidor. Para abrirlo desde otro equipo de Tailscale, activa \"Panel desde otros equipos\" en Ajustes."))
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/panel")
	switch {
	case path == "":
		http.Redirect(w, r, "/panel/", http.StatusFound)
		return
	case path == "/login":
		p.linkLogin(w, r)
		return
	case path == "/api/login":
		p.keyLogin(w, r)
		return
	case strings.HasPrefix(path, "/api/"):
		p.api(w, r, strings.TrimPrefix(path, "/api/"))
		return
	}
	if path == "/" {
		w.Header().Set("Cache-Control", "no-store")
	}
	p.static.ServeHTTP(w, r)
}

func (p *Panel) linkLogin(w http.ResponseWriter, r *http.Request) {
	if !clientIP(r).IsLoopback() || !useLoginLink(r.Context(), p.d.Store.DB, r.URL.Query().Get("t")) {
		http.Redirect(w, r, "/panel/#login-vencido", http.StatusFound)
		return
	}
	id, _ := p.sessions.create(false)
	setCookie(w, id, int(sessionTTL.Seconds()))
	p.d.Policy.Audit(r.Context(), policy.ActorPanel, "panel.login", "", "ok", map[string]any{"method": "link"})
	target := "/panel/"
	if sec := r.URL.Query().Get("s"); sec != "" && validSection(sec) {
		target += "#" + sec
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func validSection(s string) bool {
	switch s {
	case "inicio", "vincular", "conversaciones", "lista", "ajustes", "registro", "conectar", "importar":
		return true
	}
	return false
}

func (p *Panel) keyLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		fail(w, http.StatusMethodNotAllowed, "método no permitido")
		return
	}
	ip := clientIP(r).String()
	if p.limit.blocked(ip) {
		fail(w, http.StatusTooManyRequests, "Demasiados intentos. Espera 10 minutos.")
		return
	}
	var body struct {
		Key string `json:"key"`
	}
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body)
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(body.Key)), []byte(p.d.Cfg.AuthToken)) != 1 {
		p.limit.fail(ip)
		time.Sleep(500 * time.Millisecond)
		p.d.Policy.Audit(r.Context(), policy.ActorPanel, "panel.login", ip, "denied", nil)
		fail(w, http.StatusUnauthorized, "La clave no es correcta.")
		return
	}
	remote := !clientIP(r).IsLoopback()
	id, _ := p.sessions.create(remote)
	setCookie(w, id, int(sessionTTL.Seconds()))
	p.d.Policy.Audit(r.Context(), policy.ActorPanel, "panel.login", ip, "ok", map[string]any{"method": "key", "remote": remote})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// api dispatches the JSON calls. Every call needs a session; changes also
// need the CSRF token the page got from /api/me.
func (p *Panel) api(w http.ResponseWriter, r *http.Request, name string) {
	sess := p.sessions.get(r)
	if sess == nil {
		fail(w, http.StatusUnauthorized, "login")
		return
	}
	if r.Method != http.MethodGet {
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("X-CSRF-Token")), []byte(sess.csrf)) != 1 {
			fail(w, http.StatusForbidden, "La página caducó. Recárgala.")
			return
		}
		if o := r.Header.Get("Origin"); o != "" && !strings.HasSuffix(o, "//"+r.Host) {
			fail(w, http.StatusForbidden, "origen no permitido")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	}
	ctx := r.Context()
	key := name + " " + r.Method
	switch key {
	case "me GET":
		writeJSON(w, 200, map[string]any{"ok": true, "csrf": sess.csrf, "remote": sess.remote, "version": version.Version})
	case "logout POST":
		p.sessions.drop(r)
		setCookie(w, "", -1)
		writeJSON(w, 200, map[string]any{"ok": true})
	case "status GET":
		p.status(ctx, w)
	case "pair GET":
		p.pairState(w)
	case "pair POST":
		p.pairStart(ctx, w, r)
	case "pair/cancel POST":
		p.d.Bridge.CancelPairing()
		writeJSON(w, 200, map[string]any{"ok": true})
	case "unlink POST":
		p.unlink(ctx, w)
	case "chats GET":
		p.chats(ctx, w, r)
	case "chats/meta POST":
		p.chatMeta(ctx, w, r)
	case "approved GET":
		p.approvedList(ctx, w)
	case "approved POST":
		p.approvedAdd(ctx, w, r)
	case "approved/remove POST":
		p.approvedRemove(ctx, w, r)
	case "settings GET":
		p.settings(ctx, w)
	case "settings POST":
		p.settingSet(ctx, w, r)
	case "audit GET":
		p.audit(ctx, w, r)
	case "connection GET":
		p.connection(w, r)
	case "import POST":
		p.importHistory(ctx, w, r)
	case "cloud/funnel POST":
		p.cloudFunnel(ctx, w, r)
	case "cloud/rotate POST":
		p.cloudRotate(ctx, w)
	default:
		fail(w, http.StatusNotFound, "no existe")
	}
}

func decode(r *http.Request, v any) error {
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		return errors.New("datos no válidos")
	}
	return nil
}

func (p *Panel) status(ctx context.Context, w http.ResponseWriter) {
	st := p.d.Bridge.Status()
	level := "red"
	switch {
	case st.Paired && st.Connected && st.LoggedIn:
		level = "green"
	case st.Paired:
		level = "amber"
	}
	var chats, msgs, approved int
	dbOK := p.d.Store.DB.PingContext(ctx) == nil
	_ = p.d.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM wa_chats`).Scan(&chats)
	_ = p.d.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM wa_messages`).Scan(&msgs)
	_ = p.d.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM approved_recipients`).Scan(&approved)
	var unanswered int
	_ = p.d.Store.DB.QueryRowContext(ctx, `SELECT count(*) FROM wa_chats c
		JOIN LATERAL (SELECT is_from_me FROM wa_messages WHERE chat_jid = c.jid ORDER BY timestamp DESC LIMIT 1) lm ON true
		WHERE lm.is_from_me = false AND c.jid NOT LIKE '%@g.us' AND c.jid NOT LIKE '%@broadcast' AND c.jid NOT LIKE '%@newsletter'
		AND c.last_message_time > now() - interval '30 days'`).Scan(&unanswered)
	s, _ := p.d.Policy.Settings(ctx)
	var ts []string
	for _, ip := range p.d.TailscaleIPs() {
		if ip.To4() != nil {
			ts = append(ts, ip.String())
		}
	}
	out := map[string]any{"ok": true, "level": level, "summary": st.Summary(), "whatsapp": st, "number": st.Number(),
		"version": version.Version, "db_ok": dbOK, "chats": chats, "messages": msgs, "approved": approved,
		"unanswered_30d": unanswered, "tailscale": ts, "port": p.d.Cfg.ListenPort,
		"sending_enabled": s.SendingEnabled, "panel_remote": s.PanelRemote, "pairing": p.d.Bridge.PairStatus(),
		"cloud": map[string]any{"enabled": s.CloudAccessEnabled, "available": p.d.Cloud != nil}}
	writeJSON(w, 200, out)
}

func (p *Panel) pairState(w http.ResponseWriter) {
	st := p.d.Bridge.PairStatus()
	out := map[string]any{"ok": true, "state": st.State, "mode": st.Mode, "code": st.Code, "error": st.Error,
		"paired": p.d.Bridge.Paired(), "summary": p.d.Bridge.Status().Summary()}
	if st.State == "qr" && st.QR != "" {
		if c, err := qr.Encode(st.QR, qr.L); err == nil {
			c.Scale = 8
			out["qr_png"] = base64.StdEncoding.EncodeToString(c.PNG())
		}
	}
	writeJSON(w, 200, out)
}

func (p *Panel) pairStart(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode  string `json:"mode"`
		Phone string `json:"phone"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	phone := ""
	if body.Mode == "phone" {
		phone = mstore.DigitsOnly(body.Phone)
		if len(phone) < 8 || len(phone) > 15 {
			fail(w, 400, "Escribe el número completo con el código de país.")
			return
		}
	}
	if err := p.d.Bridge.StartPairing(phone, policy.ActorPanel); err != nil {
		if errors.Is(err, wa.ErrAlreadyPaired) {
			fail(w, 409, "Ya hay un WhatsApp vinculado.")
			return
		}
		fail(w, 409, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) unlink(ctx context.Context, w http.ResponseWriter) {
	acct := p.d.Bridge.Status().Account
	if err := p.d.Bridge.UnlinkHere(ctx); err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "session.unlinked", acct, "error", map[string]any{"error": err.Error()})
		fail(w, 500, "No se pudo desvincular: "+err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "session.unlinked", acct, "ok", nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// approvedSet loads the approved JIDs.
func (p *Panel) approvedSet(ctx context.Context) (map[string]policy.Recipient, error) {
	rs, err := p.d.Policy.ListApproved(ctx)
	if err != nil {
		return nil, err
	}
	m := make(map[string]policy.Recipient, len(rs))
	for _, r := range rs {
		m[r.JID] = r
	}
	return m, nil
}

type chatRow struct {
	mstore.Conversation
	Approved bool   `json:"approved"`
	SendTo   string `json:"send_to"` // JID added to the approved list
}

func (p *Panel) chats(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f := mstore.ConvFilter{Query: q.Get("q"), Kind: q.Get("kind"), Category: q.Get("category"), Country: q.Get("country"),
		Unanswered: q.Get("unanswered") == "1"}
	f.Limit, _ = strconv.Atoi(q.Get("limit"))
	f.Offset, _ = strconv.Atoi(q.Get("offset"))
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 60
	}
	onlyApproved := q.Get("approved") == "1"
	approved, err := p.approvedSet(ctx)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	limit, offset := f.Limit, f.Offset
	if onlyApproved {
		f.Limit, f.Offset = 2000, 0
	}
	convs, err := p.d.Store.ListConversations(ctx, f)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	rows := make([]chatRow, 0, len(convs))
	for _, c := range convs {
		row := chatRow{Conversation: c, SendTo: c.JID}
		if canon, aliases, err := p.d.Bridge.Resolve(ctx, c.JID); err == nil {
			row.SendTo = canon
			for _, a := range aliases {
				if _, ok := approved[a]; ok {
					row.Approved = true
				}
			}
			if row.Phone == "" && strings.HasSuffix(canon, "@s.whatsapp.net") {
				row.Phone = strings.SplitN(canon, "@", 2)[0]
				row.CountryGuess = mstore.SuggestCountry(row.Phone)
			}
		}
		if _, ok := approved[c.JID]; ok {
			row.Approved = true
		}
		if onlyApproved && !row.Approved {
			continue
		}
		rows = append(rows, row)
	}
	more := len(convs) == f.Limit
	if onlyApproved {
		more = offset+limit < len(rows)
		end := offset + limit
		if end > len(rows) {
			end = len(rows)
		}
		if offset > len(rows) {
			offset = len(rows)
		}
		rows = rows[offset:end]
	}
	s, _ := p.d.Policy.Settings(ctx)
	countries, _ := p.d.Store.CountryCounts(ctx)
	writeJSON(w, 200, map[string]any{"ok": true, "chats": rows, "more": more, "categories": s.Categories(), "countries": countries})
}

func (p *Panel) chatMeta(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		JID      string  `json:"jid"`
		Category *string `json:"category"`
		Country  *string `json:"country"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if body.Category != nil && *body.Category != "" {
		s, _ := p.d.Policy.Settings(ctx)
		ok := false
		for _, c := range s.Categories() {
			if strings.EqualFold(c, *body.Category) {
				*body.Category, ok = c, true
			}
		}
		if !ok {
			fail(w, 400, "Esa categoría no existe. Añádela en Ajustes.")
			return
		}
	}
	oldCat, oldCountry, err := p.d.Store.SetChatMeta(ctx, body.JID, body.Category, body.Country, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	details := map[string]any{}
	if body.Category != nil {
		details["category"] = map[string]any{"old": oldCat, "new": *body.Category}
	}
	if body.Country != nil {
		details["country"] = map[string]any{"old": oldCountry, "new": strings.ToUpper(*body.Country)}
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "chat.meta", body.JID, "ok", details)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) approvedList(ctx context.Context, w http.ResponseWriter) {
	rs, err := p.d.Policy.ListApproved(ctx)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "approved": rs})
}

func (p *Panel) approvedAdd(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Recipient string `json:"recipient"` // number or JID
		Label     string `json:"label"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	target := body.Recipient
	if canon, _, err := p.d.Bridge.Resolve(ctx, body.Recipient); err == nil {
		target = canon
	}
	rec, err := p.d.Policy.AddApproved(ctx, target, strings.TrimSpace(body.Label), policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "recipient": rec})
}

func (p *Panel) approvedRemove(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Recipient string `json:"recipient"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	targets := []string{body.Recipient}
	if _, aliases, err := p.d.Bridge.Resolve(ctx, body.Recipient); err == nil {
		targets = append(targets, aliases...)
	}
	removed := false
	seen := map[string]bool{}
	for _, t := range targets {
		if seen[t] {
			continue
		}
		seen[t] = true
		ok, err := p.d.Policy.RemoveApproved(ctx, t, policy.ActorPanel)
		if err != nil {
			fail(w, 400, err.Error())
			return
		}
		removed = removed || ok
	}
	writeJSON(w, 200, map[string]any{"ok": true, "removed": removed})
}

func (p *Panel) settings(ctx context.Context, w http.ResponseWriter) {
	views, err := p.d.Policy.ListSettings(ctx)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	type item struct {
		policy.SettingView
		Kind string `json:"kind"`
		Min  int    `json:"min,omitempty"`
		Max  int    `json:"max,omitempty"`
	}
	out := make([]item, 0, len(views))
	for _, v := range views {
		d, _ := policy.DefByKey(v.Key)
		out = append(out, item{SettingView: v, Kind: kindName(d.Kind), Min: d.Min, Max: d.Max})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "settings": out})
}

func kindName(k policy.Kind) string {
	switch k {
	case policy.Bool:
		return "bool"
	case policy.Int:
		return "int"
	}
	return "string"
}

func (p *Panel) settingSet(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key   string `json:"key"`
		Value string `json:"value"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	old, nv, err := p.d.Policy.SetSetting(ctx, body.Key, body.Value, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "old": old, "new": nv})
}

func (p *Panel) audit(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 500 {
		limit = 150
	}
	es, err := p.d.Policy.ListAudit(ctx, limit, r.URL.Query().Get("action"))
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "entries": es})
}

func (p *Panel) connection(w http.ResponseWriter, r *http.Request) {
	var urls []string
	for _, ip := range p.d.TailscaleIPs() {
		urls = append(urls, "http://"+net.JoinHostPort(ip.String(), strconv.Itoa(p.d.Cfg.ListenPort))+"/mcp")
	}
	sort.Strings(urls)
	out := map[string]any{"ok": true, "urls": urls, "token": p.d.Cfg.AuthToken}
	if p.d.Cloud != nil {
		out["cloud"] = p.d.Cloud.Info(r.Context())
	}
	p.d.Policy.Audit(r.Context(), policy.ActorPanel, "panel.show_key", "", "ok", nil)
	writeJSON(w, 200, out)
}

func (p *Panel) importHistory(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Path string `json:"path"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	path := strings.Trim(strings.TrimSpace(body.Path), `"`)
	if !strings.HasSuffix(strings.ToLower(path), ".db") {
		fail(w, 400, "Elige el archivo messages.db.")
		return
	}
	ictx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	res, err := sqlitemigrate.Import(ictx, path, p.d.Store.DB, nil)
	if err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "history.import", path, "error", map[string]any{"error": err.Error()})
		fail(w, 400, "No se pudo importar: "+err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "history.import", path, "ok", map[string]any{"chats": res.ChatsImported, "messages": res.MessagesImported})
	writeJSON(w, 200, map[string]any{"ok": true, "result": res})
}

func (p *Panel) cloudFunnel(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	if p.d.Cloud == nil {
		fail(w, 404, "no disponible")
		return
	}
	var body struct {
		On bool `json:"on"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	action := "cloud.funnel_off"
	if body.On {
		action = "cloud.funnel_on"
	}
	if err := p.d.Cloud.SetFunnel(ctx, body.On); err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, action, "", "error", map[string]any{"error": err.Error()})
		fail(w, 500, "Tailscale respondió: "+err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, action, "", "ok", nil)
	writeJSON(w, 200, map[string]any{"ok": true, "cloud": p.d.Cloud.Info(ctx)})
}

func (p *Panel) cloudRotate(ctx context.Context, w http.ResponseWriter) {
	if p.d.Cloud == nil {
		fail(w, 404, "no disponible")
		return
	}
	if _, err := p.d.Cloud.RotateKey(); err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "cloud.key_rotated", "", "error", map[string]any{"error": err.Error()})
		fail(w, 500, "No se pudo cambiar la clave: "+err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "cloud.key_rotated", "", "ok", nil)
	writeJSON(w, 200, map[string]any{"ok": true, "cloud": p.d.Cloud.Info(ctx)})
}
