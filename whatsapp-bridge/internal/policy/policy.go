// Package policy holds the runtime rules: which recipients may receive
// messages, where attachments may come from, send limits, and the audit
// log that records every change and every send attempt.
//
// The rules live in PostgreSQL. They can be changed by hand (command line
// on the server) or by Claude through MCP tools when the owner asks, and
// every change is written to audit_log with date, actor and old/new value.
package policy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Kind of a setting value.
type Kind int

const (
	Bool Kind = iota
	Int
	String
)

// Def describes one setting.
type Def struct {
	Key         string
	Kind        Kind
	Default     any
	Description string
	// LockedFromMCP settings can only be changed by hand on the server,
	// never by Claude (otherwise a tricked Claude could unlock itself).
	LockedFromMCP bool
	Min, Max      int
}

// Defs is the catalogue of runtime settings.
var Defs = []Def{
	{Key: "sending_enabled", Kind: Bool, Default: true,
		Description: "Master switch for sending messages and files. Off = read only."},
	{Key: "send_only_to_approved", Kind: Bool, Default: true, LockedFromMCP: true,
		Description: "Only send to contacts and groups on the approved list. Can only be turned off by hand on the server."},
	{Key: "attachments_folder_only", Kind: Bool, Default: false,
		Description: "Only allow sending files that are inside attachments_folder."},
	{Key: "attachments_folder", Kind: String, Default: "",
		Description: "Folder files must be in when attachments_folder_only is on."},
	{Key: "max_attachment_mb", Kind: Int, Default: 100, Min: 1, Max: 2000,
		Description: "Largest file that may be sent, in MB."},
	{Key: "max_sends_per_minute", Kind: Int, Default: 10, Min: 1, Max: 120,
		Description: "Send rate limit. Protects the number from being banned for bulk sending."},
	{Key: "mcp_can_change_settings", Kind: Bool, Default: true, LockedFromMCP: true,
		Description: "Allow Claude to change settings and the approved list through MCP when you ask. Can only be changed by hand on the server."},
	{Key: "audit_message_text", Kind: Bool, Default: false,
		Description: "Also store the text of sent messages in the audit log (off = only length)."},
	{Key: "download_media_enabled", Kind: Bool, Default: true,
		Description: "Allow downloading received photos, audios, videos and documents to the media folder."},
	{Key: "chat_categories", Kind: String, Default: "Cliente, Distribuidor, Proveedor, Personal, Otro",
		Description: "Categories you can give to each chat, separated by commas."},
	{Key: "cloud_access_enabled", Kind: Bool, Default: false, LockedFromMCP: true,
		Description: "Let Claude in the cloud reach the connector through Tailscale Funnel (needs the cloud key). Off = only Tailscale devices."},
	{Key: "panel_remote", Kind: Bool, Default: false, LockedFromMCP: true,
		Description: "Allow opening the web panel from other Tailscale devices (login with the access key). Off = only on the server itself."},
}

// DefByKey finds a definition.
func DefByKey(key string) (Def, bool) {
	for _, d := range Defs {
		if d.Key == key {
			return d, true
		}
	}
	return Def{}, false
}

// Settings is a typed snapshot of the runtime settings.
type Settings struct {
	SendingEnabled        bool   `json:"sending_enabled"`
	SendOnlyToApproved    bool   `json:"send_only_to_approved"`
	AttachmentsFolderOnly bool   `json:"attachments_folder_only"`
	AttachmentsFolder     string `json:"attachments_folder"`
	MaxAttachmentMB       int    `json:"max_attachment_mb"`
	MaxSendsPerMinute     int    `json:"max_sends_per_minute"`
	MCPCanChangeSettings  bool   `json:"mcp_can_change_settings"`
	AuditMessageText      bool   `json:"audit_message_text"`
	DownloadMediaEnabled  bool   `json:"download_media_enabled"`
	PanelRemote           bool   `json:"panel_remote"`
	ChatCategories        string `json:"chat_categories"`
	CloudAccessEnabled    bool   `json:"cloud_access_enabled"`
}

// Actor identifies who did something, for the audit log.
const (
	ActorManual    = "manual"    // command line on the server
	ActorPanel     = "panel"     // web panel (by hand)
	ActorMCP       = "claude"    // MCP tool call (Tailscale)
	ActorCloud     = "cloud"     // MCP tool call from Claude in the cloud (Funnel)
	ActorREST      = "rest"      // REST API caller
	ActorInstaller = "installer" // setup
	ActorSystem    = "system"
)

// Policy is the rules engine.
type Policy struct {
	db *sql.DB

	mu       sync.Mutex
	cached   *Settings
	cachedAt time.Time

	rlMu  sync.Mutex
	sends []time.Time
}

// New returns a Policy.
func New(d *sql.DB) *Policy { return &Policy{db: d} }

// Settings returns the current settings (cached for 5 seconds so changes
// made by hand on the server apply almost immediately).
func (p *Policy) Settings(ctx context.Context) (Settings, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cached != nil && time.Since(p.cachedAt) < 5*time.Second {
		return *p.cached, nil
	}
	raw := map[string]json.RawMessage{}
	rows, err := p.db.QueryContext(ctx, `SELECT key, value FROM app_settings`)
	if err != nil {
		return Settings{}, err
	}
	defer rows.Close()
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			return Settings{}, err
		}
		raw[k] = v
	}
	if err := rows.Err(); err != nil {
		return Settings{}, err
	}
	merged := map[string]any{}
	for _, d := range Defs {
		merged[d.Key] = d.Default
		if v, ok := raw[d.Key]; ok {
			var x any
			if json.Unmarshal(v, &x) == nil {
				merged[d.Key] = x
			}
		}
	}
	b, _ := json.Marshal(merged)
	var s Settings
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	p.cached, p.cachedAt = &s, time.Now()
	return s, nil
}

func (p *Policy) invalidate() {
	p.mu.Lock()
	p.cached = nil
	p.mu.Unlock()
}

// SettingView is one setting with its value, for listing.
type SettingView struct {
	Key         string `json:"key"`
	Value       any    `json:"value"`
	Default     any    `json:"default"`
	Description string `json:"description"`
	OnlyByHand  bool   `json:"only_by_hand"`
}

// ListSettings returns every setting with its current value.
func (p *Policy) ListSettings(ctx context.Context) ([]SettingView, error) {
	s, err := p.Settings(ctx)
	if err != nil {
		return nil, err
	}
	b, _ := json.Marshal(s)
	cur := map[string]any{}
	_ = json.Unmarshal(b, &cur)
	out := make([]SettingView, 0, len(Defs))
	for _, d := range Defs {
		out = append(out, SettingView{Key: d.Key, Value: cur[d.Key], Default: d.Default, Description: d.Description, OnlyByHand: d.LockedFromMCP})
	}
	return out, nil
}

// ErrNotAllowedFromMCP is returned when Claude tries to change something
// only the owner can change by hand.
var ErrNotAllowedFromMCP = errors.New("this setting can only be changed by hand on the server")

// IsClaude says whether the actor is Claude (over Tailscale or the cloud),
// which the hand-only rules apply to.
func IsClaude(actor string) bool { return actor == ActorMCP || actor == ActorCloud }

// CheckClaudeChange returns an error when Claude may not change things
// (mcp_can_change_settings is off). Other actors always pass.
func (p *Policy) CheckClaudeChange(ctx context.Context, actor string) error {
	return p.checkMCP(ctx, actor)
}

func (p *Policy) checkMCP(ctx context.Context, actor string) error {
	if !IsClaude(actor) {
		return nil
	}
	s, err := p.Settings(ctx)
	if err != nil {
		return err
	}
	if !s.MCPCanChangeSettings {
		return errors.New("changes from Claude are turned off (mcp_can_change_settings = false); change it by hand on the server")
	}
	return nil
}

// ParseValue converts text into the setting's type and checks it.
func ParseValue(d Def, text string) (any, error) {
	text = strings.TrimSpace(text)
	switch d.Kind {
	case Bool:
		switch strings.ToLower(text) {
		case "true", "1", "yes", "si", "sí", "on", "encendido", "activar":
			return true, nil
		case "false", "0", "no", "off", "apagado", "desactivar":
			return false, nil
		}
		return nil, fmt.Errorf("%s needs true or false", d.Key)
	case Int:
		n, err := strconv.Atoi(text)
		if err != nil {
			return nil, fmt.Errorf("%s needs a whole number", d.Key)
		}
		if (d.Min != 0 || d.Max != 0) && (n < d.Min || n > d.Max) {
			return nil, fmt.Errorf("%s must be between %d and %d", d.Key, d.Min, d.Max)
		}
		return n, nil
	default:
		if d.Key == "attachments_folder" && text != "" {
			abs, err := filepath.Abs(text)
			if err != nil {
				return nil, err
			}
			return abs, nil
		}
		return text, nil
	}
}

// SetSetting changes a setting and audits the change.
func (p *Policy) SetSetting(ctx context.Context, key, text, actor string) (old, new any, err error) {
	d, ok := DefByKey(key)
	if !ok {
		return nil, nil, fmt.Errorf("unknown setting %q", key)
	}
	if IsClaude(actor) && d.LockedFromMCP {
		p.Audit(ctx, actor, "setting.change", key, "denied", map[string]any{"reason": ErrNotAllowedFromMCP.Error(), "requested": text})
		return nil, nil, ErrNotAllowedFromMCP
	}
	if err := p.checkMCP(ctx, actor); err != nil {
		p.Audit(ctx, actor, "setting.change", key, "denied", map[string]any{"reason": err.Error(), "requested": text})
		return nil, nil, err
	}
	v, err := ParseValue(d, text)
	if err != nil {
		return nil, nil, err
	}
	views, err := p.ListSettings(ctx)
	if err != nil {
		return nil, nil, err
	}
	for _, sv := range views {
		if sv.Key == key {
			old = sv.Value
		}
	}
	b, _ := json.Marshal(v)
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `INSERT INTO app_settings (key, value, updated_at, updated_by) VALUES ($1, $2, now(), $3)
		ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now(), updated_by = EXCLUDED.updated_by`, key, b, actor); err != nil {
		return nil, nil, err
	}
	if err := auditTx(ctx, tx, actor, "setting.change", key, "ok", map[string]any{"old": old, "new": v}); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	p.invalidate()
	return old, v, nil
}

// Recipient is an approved contact or group.
type Recipient struct {
	JID     string    `json:"jid"`
	Kind    string    `json:"kind"`
	Label   string    `json:"label"`
	AddedAt time.Time `json:"added_at"`
	AddedBy string    `json:"added_by"`
}

// NormalizeRecipient turns a phone number or JID into a canonical JID and
// says whether it is a group. "+57 300 123 4567" -> "573001234567@s.whatsapp.net".
func NormalizeRecipient(in string) (jid, kind string, err error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return "", "", errors.New("empty recipient")
	}
	if strings.Contains(in, "@") {
		parts := strings.SplitN(strings.ToLower(in), "@", 2)
		user, server := parts[0], parts[1]
		// Drop any device suffix (123:4@s.whatsapp.net -> 123@s.whatsapp.net).
		if i := strings.IndexAny(user, ":."); i >= 0 && server != "g.us" {
			user = user[:i]
		}
		switch server {
		case "g.us":
			if user == "" {
				return "", "", errors.New("invalid group id")
			}
			return user + "@g.us", "group", nil
		case "s.whatsapp.net", "lid":
			if user == "" || strings.Trim(user, "0123456789") != "" {
				return "", "", fmt.Errorf("invalid contact id %q", in)
			}
			return user + "@" + server, "contact", nil
		default:
			return "", "", fmt.Errorf("unsupported WhatsApp address %q", in)
		}
	}
	digits := ""
	for _, r := range in {
		switch {
		case r >= '0' && r <= '9':
			digits += string(r)
		case r == '+' || r == ' ' || r == '-' || r == '(' || r == ')' || r == '.':
		default:
			return "", "", fmt.Errorf("%q is not a phone number", in)
		}
	}
	if len(digits) < 7 || len(digits) > 15 {
		return "", "", fmt.Errorf("%q is not a full phone number with country code", in)
	}
	return digits + "@s.whatsapp.net", "contact", nil
}

// ListApproved returns the approved recipients.
func (p *Policy) ListApproved(ctx context.Context) ([]Recipient, error) {
	rows, err := p.db.QueryContext(ctx, `SELECT jid, kind, label, added_at, added_by FROM approved_recipients ORDER BY kind, label, jid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Recipient
	for rows.Next() {
		var r Recipient
		if err := rows.Scan(&r.JID, &r.Kind, &r.Label, &r.AddedAt, &r.AddedBy); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// AddApproved puts a recipient on the list.
func (p *Policy) AddApproved(ctx context.Context, recipient, label, actor string) (Recipient, error) {
	if err := p.checkMCP(ctx, actor); err != nil {
		p.Audit(ctx, actor, "approved.add", recipient, "denied", map[string]any{"reason": err.Error()})
		return Recipient{}, err
	}
	jid, kind, err := NormalizeRecipient(recipient)
	if err != nil {
		return Recipient{}, err
	}
	label = strings.TrimSpace(label)
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return Recipient{}, err
	}
	defer tx.Rollback()
	var r Recipient
	err = tx.QueryRowContext(ctx, `INSERT INTO approved_recipients (jid, kind, label, added_by) VALUES ($1,$2,$3,$4)
		ON CONFLICT (jid) DO UPDATE SET label = CASE WHEN EXCLUDED.label <> '' THEN EXCLUDED.label ELSE approved_recipients.label END
		RETURNING jid, kind, label, added_at, added_by`, jid, kind, label, actor).
		Scan(&r.JID, &r.Kind, &r.Label, &r.AddedAt, &r.AddedBy)
	if err != nil {
		return Recipient{}, err
	}
	if err := auditTx(ctx, tx, actor, "approved.add", jid, "ok", map[string]any{"kind": kind, "label": r.Label}); err != nil {
		return Recipient{}, err
	}
	return r, tx.Commit()
}

// RemoveApproved takes a recipient off the list.
func (p *Policy) RemoveApproved(ctx context.Context, recipient, actor string) (bool, error) {
	if err := p.checkMCP(ctx, actor); err != nil {
		p.Audit(ctx, actor, "approved.remove", recipient, "denied", map[string]any{"reason": err.Error()})
		return false, err
	}
	jid, _, err := NormalizeRecipient(recipient)
	if err != nil {
		return false, err
	}
	tx, err := p.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `DELETE FROM approved_recipients WHERE jid = $1`, jid)
	if err != nil {
		return false, err
	}
	n, _ := res.RowsAffected()
	outcome := "ok"
	if n == 0 {
		outcome = "not_found"
	}
	if err := auditTx(ctx, tx, actor, "approved.remove", jid, outcome, nil); err != nil {
		return false, err
	}
	return n > 0, tx.Commit()
}

// IsApproved reports whether any of the given JIDs (a contact can be known
// by phone-number JID and by LID) is on the list.
func (p *Policy) IsApproved(ctx context.Context, jids ...string) (bool, error) {
	if len(jids) == 0 {
		return false, nil
	}
	var n int
	err := p.db.QueryRowContext(ctx, `SELECT count(*) FROM approved_recipients WHERE jid = ANY($1)`, jids).Scan(&n)
	return n > 0, err
}

// ErrDenied wraps a refusal so callers can tell it apart from failures.
type ErrDenied struct{ Reason string }

func (e *ErrDenied) Error() string { return "not allowed: " + e.Reason }

// CheckSend decides whether a send may go out. jids are all known forms of
// the recipient (first one is the canonical one).
func (p *Policy) CheckSend(ctx context.Context, jids []string) error {
	s, err := p.Settings(ctx)
	if err != nil {
		return err
	}
	if !s.SendingEnabled {
		return &ErrDenied{"sending is turned off (sending_enabled = false)"}
	}
	if s.SendOnlyToApproved {
		ok, err := p.IsApproved(ctx, jids...)
		if err != nil {
			return err
		}
		if !ok {
			return &ErrDenied{fmt.Sprintf("%s is not on the approved list; ask the owner to add it", jids[0])}
		}
	}
	return nil
}

// TakeSendSlot enforces max_sends_per_minute; call it right before sending.
func (p *Policy) TakeSendSlot(ctx context.Context) error {
	s, err := p.Settings(ctx)
	if err != nil {
		return err
	}
	return p.takeRateSlot(s.MaxSendsPerMinute)
}

func (p *Policy) takeRateSlot(max int) error {
	p.rlMu.Lock()
	defer p.rlMu.Unlock()
	cut := time.Now().Add(-time.Minute)
	kept := p.sends[:0]
	for _, t := range p.sends {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	p.sends = kept
	if max > 0 && len(p.sends) >= max {
		return &ErrDenied{fmt.Sprintf("rate limit reached (%d sends per minute)", max)}
	}
	p.sends = append(p.sends, time.Now())
	return nil
}

// CheckAttachment validates a file that is about to be sent and returns
// its cleaned absolute path.
func (p *Policy) CheckAttachment(ctx context.Context, path string) (string, error) {
	s, err := p.Settings(ctx)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(path) == "" {
		return "", errors.New("file path is empty")
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	// Resolve symlinks/junctions so the folder rule cannot be bypassed.
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", abs)
	}
	st, err := os.Stat(real)
	if err != nil {
		return "", fmt.Errorf("file not found: %s", abs)
	}
	if !st.Mode().IsRegular() {
		return "", &ErrDenied{"only regular files can be sent"}
	}
	if s.MaxAttachmentMB > 0 && st.Size() > int64(s.MaxAttachmentMB)<<20 {
		return "", &ErrDenied{fmt.Sprintf("file is larger than %d MB (max_attachment_mb)", s.MaxAttachmentMB)}
	}
	if s.AttachmentsFolderOnly {
		if s.AttachmentsFolder == "" {
			return "", &ErrDenied{"attachments_folder_only is on but attachments_folder is empty"}
		}
		base, err := filepath.EvalSymlinks(s.AttachmentsFolder)
		if err != nil {
			return "", &ErrDenied{"attachments folder does not exist: " + s.AttachmentsFolder}
		}
		if !Within(base, real) {
			return "", &ErrDenied{fmt.Sprintf("only files inside %s can be sent (attachments_folder_only)", s.AttachmentsFolder)}
		}
	}
	return real, nil
}

// Within reports whether target is inside base (case-insensitive on Windows).
func Within(base, target string) bool {
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return false
	}
	if rel == "." {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}

// Audit writes an audit entry; failures are logged by the caller only.
func (p *Policy) Audit(ctx context.Context, actor, action, target, outcome string, details map[string]any) {
	_ = auditTx(ctx, p.db, actor, action, target, outcome, details)
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func auditTx(ctx context.Context, e execer, actor, action, target, outcome string, details map[string]any) error {
	if details == nil {
		details = map[string]any{}
	}
	b, err := json.Marshal(details)
	if err != nil {
		b = []byte("{}")
	}
	_, err = e.ExecContext(ctx, `INSERT INTO audit_log (actor, action, target, outcome, details) VALUES ($1,$2,$3,$4,$5)`,
		actor, action, target, outcome, b)
	return err
}

// AuditEntry is one row of the audit log.
type AuditEntry struct {
	ID      int64          `json:"id"`
	At      time.Time      `json:"at"`
	Actor   string         `json:"actor"`
	Action  string         `json:"action"`
	Target  string         `json:"target"`
	Outcome string         `json:"outcome"`
	Details map[string]any `json:"details"`
}

// ListAudit returns the newest audit entries, optionally filtered by action prefix.
func (p *Policy) ListAudit(ctx context.Context, limit int, actionPrefix string) ([]AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	rows, err := p.db.QueryContext(ctx, `SELECT id, at, actor, action, target, outcome, details FROM audit_log
		WHERE ($1 = '' OR action LIKE $1 || '%') ORDER BY id DESC LIMIT $2`, actionPrefix, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var a AuditEntry
		var d []byte
		if err := rows.Scan(&a.ID, &a.At, &a.Actor, &a.Action, &a.Target, &a.Outcome, &d); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(d, &a.Details)
		out = append(out, a)
	}
	return out, rows.Err()
}

// SortedKeys is a helper for stable output.
func SortedKeys(m map[string]any) []string {
	k := make([]string, 0, len(m))
	for x := range m {
		k = append(k, x)
	}
	sort.Strings(k)
	return k
}

// Categories returns the category list from chat_categories.
func (s Settings) Categories() []string {
	var out []string
	seen := map[string]bool{}
	for _, c := range strings.Split(s.ChatCategories, ",") {
		c = strings.TrimSpace(c)
		if c != "" && !seen[strings.ToLower(c)] {
			seen[strings.ToLower(c)] = true
			out = append(out, c)
		}
	}
	return out
}
