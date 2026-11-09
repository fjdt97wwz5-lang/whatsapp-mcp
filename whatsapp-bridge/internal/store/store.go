// Package store reads and writes chats and messages in PostgreSQL.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// Store wraps the shared database handle.
type Store struct {
	DB *sql.DB
}

// New returns a Store.
func New(d *sql.DB) *Store { return &Store{DB: d} }

// Message is one stored WhatsApp message.
type Message struct {
	ID        string    `json:"id"`
	ChatJID   string    `json:"chat_jid"`
	ChatName  string    `json:"chat_name,omitempty"`
	Sender    string    `json:"sender"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	IsFromMe  bool      `json:"is_from_me"`
	MediaType string    `json:"media_type,omitempty"`
	Filename  string    `json:"filename,omitempty"`
}

// Chat is one conversation (person or group).
type Chat struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name"`
	LastMessageTime *time.Time `json:"last_message_time,omitempty"`
	LastMessage     string     `json:"last_message,omitempty"`
	LastSender      string     `json:"last_sender,omitempty"`
	LastIsFromMe    bool       `json:"last_is_from_me,omitempty"`
	IsGroup         bool       `json:"is_group"`
}

// Contact is a person found in the chats table.
type Contact struct {
	PhoneNumber string `json:"phone_number"`
	Name        string `json:"name"`
	JID         string `json:"jid"`
}

// MediaInfo is what is needed to download an attachment later.
type MediaInfo struct {
	MediaType     string
	Filename      string
	URL           string
	MediaKey      []byte
	FileSHA256    []byte
	FileEncSHA256 []byte
	FileLength    uint64
}

// StoreChat inserts or updates a chat. The name is only replaced by a
// non-empty one and the last message time only moves forward, so an old
// history sync never makes a chat look older than it is.
func (s *Store) StoreChat(ctx context.Context, jid, name string, last time.Time) error {
	var lastArg any
	if !last.IsZero() {
		lastArg = last
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO wa_chats (jid, name, last_message_time) VALUES ($1, $2, $3)
		ON CONFLICT (jid) DO UPDATE SET
			name = COALESCE(NULLIF(EXCLUDED.name, ''), wa_chats.name),
			last_message_time = GREATEST(wa_chats.last_message_time, EXCLUDED.last_message_time)`,
		jid, name, lastArg)
	return err
}

// ChatName returns the stored name of a chat, or "" if unknown.
func (s *Store) ChatName(ctx context.Context, jid string) string {
	var name sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT name FROM wa_chats WHERE jid = $1`, jid).Scan(&name)
	return name.String
}

// StoreMessage inserts or replaces a message (only if it has text or media).
func (s *Store) StoreMessage(ctx context.Context, m Message, mi MediaInfo) error {
	if m.Content == "" && mi.MediaType == "" {
		return nil
	}
	_, err := s.DB.ExecContext(ctx, `
		INSERT INTO wa_messages (id, chat_jid, sender, content, timestamp, is_from_me,
			media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
		ON CONFLICT (id, chat_jid) DO UPDATE SET
			sender = EXCLUDED.sender, content = EXCLUDED.content, timestamp = EXCLUDED.timestamp,
			is_from_me = EXCLUDED.is_from_me, media_type = EXCLUDED.media_type,
			filename = EXCLUDED.filename, url = EXCLUDED.url, media_key = EXCLUDED.media_key,
			file_sha256 = EXCLUDED.file_sha256, file_enc_sha256 = EXCLUDED.file_enc_sha256,
			file_length = EXCLUDED.file_length`,
		m.ID, m.ChatJID, m.Sender, m.Content, m.Timestamp, m.IsFromMe,
		nullIfEmpty(mi.MediaType), nullIfEmpty(mi.Filename), nullIfEmpty(mi.URL),
		mi.MediaKey, mi.FileSHA256, mi.FileEncSHA256, int64(mi.FileLength))
	return err
}

// GetMediaInfo returns the download data of a message.
func (s *Store) GetMediaInfo(ctx context.Context, id, chatJID string) (MediaInfo, error) {
	var mi MediaInfo
	var mt, fn, url sql.NullString
	var fl sql.NullInt64
	err := s.DB.QueryRowContext(ctx, `
		SELECT media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length
		FROM wa_messages WHERE id = $1 AND chat_jid = $2`, id, chatJID).
		Scan(&mt, &fn, &url, &mi.MediaKey, &mi.FileSHA256, &mi.FileEncSHA256, &fl)
	if errors.Is(err, sql.ErrNoRows) {
		return mi, fmt.Errorf("message %s not found in chat %s", id, chatJID)
	}
	mi.MediaType, mi.Filename, mi.URL, mi.FileLength = mt.String, fn.String, url.String, uint64(fl.Int64)
	return mi, err
}

// ListFilter selects messages.
type ListFilter struct {
	After, Before *time.Time
	Sender        string
	ChatJID       string
	Query         string
	Limit, Page   int
}

const msgCols = `m.id, m.chat_jid, COALESCE(c.name,''), COALESCE(m.sender,''), COALESCE(m.content,''),
	m.timestamp, m.is_from_me, COALESCE(m.media_type,''), COALESCE(m.filename,'')`

func scanMessages(rows *sql.Rows) ([]Message, error) {
	defer rows.Close()
	var out []Message
	for rows.Next() {
		var m Message
		var ts sql.NullTime
		if err := rows.Scan(&m.ID, &m.ChatJID, &m.ChatName, &m.Sender, &m.Content, &ts, &m.IsFromMe, &m.MediaType, &m.Filename); err != nil {
			return nil, err
		}
		m.Timestamp = ts.Time
		out = append(out, m)
	}
	return out, rows.Err()
}

// ListMessages returns messages newest first.
func (s *Store) ListMessages(ctx context.Context, f ListFilter) ([]Message, error) {
	if f.Limit <= 0 || f.Limit > 500 {
		f.Limit = 20
	}
	if f.Page < 0 {
		f.Page = 0
	}
	var where []string
	var args []any
	add := func(clause string, v any) {
		args = append(args, v)
		where = append(where, fmt.Sprintf(clause, len(args)))
	}
	if f.After != nil {
		add("m.timestamp > $%d", *f.After)
	}
	if f.Before != nil {
		add("m.timestamp < $%d", *f.Before)
	}
	if f.Sender != "" {
		add("m.sender = $%d", f.Sender)
	}
	if f.ChatJID != "" {
		add("m.chat_jid = $%d", f.ChatJID)
	}
	if f.Query != "" {
		add("m.content ILIKE $%d", "%"+escapeLike(f.Query)+"%")
	}
	q := `SELECT ` + msgCols + ` FROM wa_messages m JOIN wa_chats c ON m.chat_jid = c.jid`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit, f.Page*f.Limit)
	q += fmt.Sprintf(" ORDER BY m.timestamp DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanMessages(rows)
}

// MessageContext is a message with its neighbours in the same chat.
type MessageContext struct {
	Message Message   `json:"message"`
	Before  []Message `json:"before"`
	After   []Message `json:"after"`
}

// GetMessageContext returns a message and up to before/after neighbours.
func (s *Store) GetMessageContext(ctx context.Context, id string, before, after int) (*MessageContext, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT `+msgCols+` FROM wa_messages m JOIN wa_chats c ON m.chat_jid = c.jid WHERE m.id = $1 LIMIT 1`, id)
	if err != nil {
		return nil, err
	}
	target, err := scanMessages(rows)
	if err != nil {
		return nil, err
	}
	if len(target) == 0 {
		return nil, fmt.Errorf("message with ID %s not found", id)
	}
	mc := &MessageContext{Message: target[0]}
	if before > 0 {
		rows, err = s.DB.QueryContext(ctx, `SELECT `+msgCols+` FROM wa_messages m JOIN wa_chats c ON m.chat_jid = c.jid
			WHERE m.chat_jid = $1 AND m.timestamp < $2 ORDER BY m.timestamp DESC LIMIT $3`, mc.Message.ChatJID, mc.Message.Timestamp, before)
		if err != nil {
			return nil, err
		}
		if mc.Before, err = scanMessages(rows); err != nil {
			return nil, err
		}
		// oldest first reads naturally
		for i, j := 0, len(mc.Before)-1; i < j; i, j = i+1, j-1 {
			mc.Before[i], mc.Before[j] = mc.Before[j], mc.Before[i]
		}
	}
	if after > 0 {
		rows, err = s.DB.QueryContext(ctx, `SELECT `+msgCols+` FROM wa_messages m JOIN wa_chats c ON m.chat_jid = c.jid
			WHERE m.chat_jid = $1 AND m.timestamp > $2 ORDER BY m.timestamp ASC LIMIT $3`, mc.Message.ChatJID, mc.Message.Timestamp, after)
		if err != nil {
			return nil, err
		}
		if mc.After, err = scanMessages(rows); err != nil {
			return nil, err
		}
	}
	return mc, nil
}

const chatSelect = `SELECT c.jid, COALESCE(c.name,''), c.last_message_time,
	COALESCE(lm.content,''), COALESCE(lm.sender,''), COALESCE(lm.is_from_me,false)
	FROM wa_chats c
	LEFT JOIN LATERAL (
		SELECT content, sender, is_from_me FROM wa_messages
		WHERE chat_jid = c.jid ORDER BY timestamp DESC LIMIT 1
	) lm ON $1::bool`

func scanChats(rows *sql.Rows) ([]Chat, error) {
	defer rows.Close()
	var out []Chat
	for rows.Next() {
		var c Chat
		var ts sql.NullTime
		if err := rows.Scan(&c.JID, &c.Name, &ts, &c.LastMessage, &c.LastSender, &c.LastIsFromMe); err != nil {
			return nil, err
		}
		if ts.Valid {
			t := ts.Time
			c.LastMessageTime = &t
		}
		c.IsGroup = strings.HasSuffix(c.JID, "@g.us")
		out = append(out, c)
	}
	return out, rows.Err()
}

// ListChats returns chats, most recently active first unless sortBy=="name".
func (s *Store) ListChats(ctx context.Context, query string, limit, page int, withLast bool, sortBy string) ([]Chat, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	if page < 0 {
		page = 0
	}
	args := []any{withLast}
	q := chatSelect
	if query != "" {
		args = append(args, "%"+escapeLike(query)+"%")
		q += " WHERE (c.name ILIKE $2 OR c.jid ILIKE $2)"
	}
	if sortBy == "name" {
		q += " ORDER BY c.name ASC"
	} else {
		q += " ORDER BY c.last_message_time DESC NULLS LAST"
	}
	args = append(args, limit, page*limit)
	q += fmt.Sprintf(" LIMIT $%d OFFSET $%d", len(args)-1, len(args))
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	return scanChats(rows)
}

// GetChat returns one chat or nil.
func (s *Store) GetChat(ctx context.Context, jid string, withLast bool) (*Chat, error) {
	rows, err := s.DB.QueryContext(ctx, chatSelect+" WHERE c.jid = $2", withLast, jid)
	if err != nil {
		return nil, err
	}
	cs, err := scanChats(rows)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// DirectChatByPhone finds the 1:1 chat whose JID contains the number.
func (s *Store) DirectChatByPhone(ctx context.Context, phone string) (*Chat, error) {
	phone = DigitsOnly(phone)
	if phone == "" {
		return nil, errors.New("phone number is empty")
	}
	rows, err := s.DB.QueryContext(ctx, chatSelect+` WHERE c.jid LIKE $2 AND c.jid NOT LIKE '%@g.us' LIMIT 1`, true, "%"+phone+"%")
	if err != nil {
		return nil, err
	}
	cs, err := scanChats(rows)
	if err != nil || len(cs) == 0 {
		return nil, err
	}
	return &cs[0], nil
}

// SearchContacts finds people (not groups) by name or number.
func (s *Store) SearchContacts(ctx context.Context, query string) ([]Contact, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT jid, COALESCE(name,'') FROM wa_chats
		WHERE (name ILIKE $1 OR jid ILIKE $1) AND jid NOT LIKE '%@g.us'
		ORDER BY name, jid LIMIT 50`, "%"+escapeLike(query)+"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Contact
	for rows.Next() {
		var c Contact
		if err := rows.Scan(&c.JID, &c.Name); err != nil {
			return nil, err
		}
		c.PhoneNumber = strings.SplitN(c.JID, "@", 2)[0]
		out = append(out, c)
	}
	return out, rows.Err()
}

// ContactChats returns chats where the contact took part.
func (s *Store) ContactChats(ctx context.Context, jid string, limit, page int) ([]Chat, error) {
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	user := strings.SplitN(jid, "@", 2)[0]
	rows, err := s.DB.QueryContext(ctx, chatSelect+` WHERE c.jid = $2 OR EXISTS (
			SELECT 1 FROM wa_messages x WHERE x.chat_jid = c.jid AND (x.sender = $2 OR x.sender = $3))
		ORDER BY c.last_message_time DESC NULLS LAST LIMIT $4 OFFSET $5`, true, jid, user, limit, page*limit)
	if err != nil {
		return nil, err
	}
	return scanChats(rows)
}

// LastInteraction returns the most recent message with a contact.
func (s *Store) LastInteraction(ctx context.Context, jid string) (*Message, error) {
	user := strings.SplitN(jid, "@", 2)[0]
	rows, err := s.DB.QueryContext(ctx, `SELECT `+msgCols+` FROM wa_messages m JOIN wa_chats c ON m.chat_jid = c.jid
		WHERE m.sender = $1 OR m.sender = $2 OR c.jid = $1 ORDER BY m.timestamp DESC LIMIT 1`, jid, user)
	if err != nil {
		return nil, err
	}
	ms, err := scanMessages(rows)
	if err != nil || len(ms) == 0 {
		return nil, err
	}
	return &ms[0], nil
}

// SenderName resolves a sender (number or JID) to a chat name if known.
func (s *Store) SenderName(ctx context.Context, sender string) string {
	if sender == "" {
		return ""
	}
	if n := s.ChatName(ctx, sender); n != "" {
		return n
	}
	user := strings.SplitN(sender, "@", 2)[0]
	var name sql.NullString
	_ = s.DB.QueryRowContext(ctx, `SELECT name FROM wa_chats WHERE jid = $1 OR jid = $2 LIMIT 1`,
		user+"@s.whatsapp.net", user+"@lid").Scan(&name)
	if name.String != "" {
		return name.String
	}
	return sender
}

// DigitsOnly keeps 0-9 (turns "+57 300-123 4567" into "573001234567").
func DigitsOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}
