// Package sqlitemigrate imports the history kept by the original bridge
// (store/messages.db, SQLite) into PostgreSQL. It is safe to run more than
// once: rows that already exist are skipped.
package sqlitemigrate

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite" // pure Go SQLite driver ("sqlite"), no C compiler needed

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// Result counts what was imported.
type Result struct {
	ChatsRead, ChatsImported       int
	MessagesRead, MessagesImported int
	MessagesSkipped                int // no usable date
}

// Progress is called now and then with messages done / total.
type Progress func(done, total int)

var layouts = []string{
	"2006-01-02 15:04:05.999999999-07:00",
	"2006-01-02T15:04:05.999999999-07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02T15:04:05.999999999",
	"2006-01-02 15:04:05",
	time.RFC3339Nano,
}

func toTime(v any) (*time.Time, error) {
	switch t := v.(type) {
	case nil:
		return nil, nil
	case time.Time:
		return &t, nil
	case int64:
		x := time.Unix(t, 0)
		return &x, nil
	case []byte:
		return toTime(string(t))
	case string:
		s := strings.TrimSpace(t)
		if s == "" {
			return nil, nil
		}
		// go-sqlite3 may append " +0000 UTC"-style suffixes from time.String().
		if i := strings.Index(s, " m="); i > 0 {
			s = s[:i]
		}
		for _, l := range layouts {
			if x, err := time.Parse(l, s); err == nil {
				return &x, nil
			}
		}
		if x, err := time.Parse("2006-01-02 15:04:05.999999999 -0700 MST", s); err == nil {
			return &x, nil
		}
		return nil, fmt.Errorf("unrecognised timestamp %q", s)
	}
	return nil, fmt.Errorf("unrecognised timestamp type %T", v)
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func blob(v any) []byte {
	switch t := v.(type) {
	case []byte:
		return t
	case string:
		return []byte(t)
	}
	return nil
}

func boolean(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case int64:
		return t != 0
	case string:
		return t == "1" || strings.EqualFold(t, "true")
	}
	return false
}

func i64(v any) int64 {
	switch t := v.(type) {
	case int64:
		return t
	case float64:
		return int64(t)
	}
	return 0
}

// Import copies chats and messages from the SQLite file into pg.
func Import(ctx context.Context, sqlitePath string, pg *sql.DB, progress Progress) (*Result, error) {
	abs, err := filepath.Abs(sqlitePath)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(abs); err != nil {
		return nil, fmt.Errorf("cannot find %s: %w", abs, err)
	}
	dsn := "file:" + (&url.URL{Path: filepath.ToSlash(abs)}).EscapedPath() + "?mode=ro&_pragma=busy_timeout(5000)"
	src, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	defer src.Close()
	if err := src.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("cannot open %s: %w", abs, err)
	}
	var hasTables int
	_ = src.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name IN ('chats','messages')`).Scan(&hasTables)
	if hasTables != 2 {
		return nil, fmt.Errorf("%s does not look like the bridge's messages.db (tables chats/messages missing)", abs)
	}
	res := &Result{}

	// Chats
	rows, err := src.QueryContext(ctx, `SELECT jid, name, last_message_time FROM chats`)
	if err != nil {
		return nil, err
	}
	tx, err := pg.BeginTx(ctx, nil)
	if err != nil {
		rows.Close()
		return nil, err
	}
	for rows.Next() {
		var jid, name, last any
		if err := rows.Scan(&jid, &name, &last); err != nil {
			rows.Close()
			tx.Rollback()
			return nil, err
		}
		res.ChatsRead++
		ts, _ := toTime(last)
		var tsArg any
		if ts != nil {
			tsArg = *ts
		}
		r, err := tx.ExecContext(ctx, `INSERT INTO wa_chats (jid, name, last_message_time) VALUES ($1,$2,$3)
			ON CONFLICT (jid) DO UPDATE SET
				name = COALESCE(NULLIF(wa_chats.name,''), EXCLUDED.name),
				last_message_time = GREATEST(wa_chats.last_message_time, EXCLUDED.last_message_time)`,
			str(jid), str(name), tsArg)
		if err != nil {
			rows.Close()
			tx.Rollback()
			return nil, fmt.Errorf("chat %v: %w", jid, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.ChatsImported++
		}
	}
	rows.Close()
	if err := tx.Commit(); err != nil {
		return nil, err
	}

	// Messages
	var total int
	_ = src.QueryRowContext(ctx, `SELECT count(*) FROM messages`).Scan(&total)
	rows, err = src.QueryContext(ctx, `SELECT id, chat_jid, sender, content, timestamp, is_from_me, media_type,
		filename, url, media_key, file_sha256, file_enc_sha256, file_length FROM messages`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	const batch = 2000
	tx, err = pg.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO wa_messages (id, chat_jid, sender, content, timestamp, is_from_me,
		media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (id, chat_jid) DO NOTHING`)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	ensureChat, err := tx.PrepareContext(ctx, `INSERT INTO wa_chats (jid) VALUES ($1) ON CONFLICT DO NOTHING`)
	if err != nil {
		tx.Rollback()
		return nil, err
	}
	commit := func(reopen bool) error {
		stmt.Close()
		ensureChat.Close()
		if err := tx.Commit(); err != nil {
			return err
		}
		if !reopen {
			return nil
		}
		if tx, err = pg.BeginTx(ctx, nil); err != nil {
			return err
		}
		if stmt, err = tx.PrepareContext(ctx, `INSERT INTO wa_messages (id, chat_jid, sender, content, timestamp, is_from_me,
			media_type, filename, url, media_key, file_sha256, file_enc_sha256, file_length)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) ON CONFLICT (id, chat_jid) DO NOTHING`); err != nil {
			return err
		}
		ensureChat, err = tx.PrepareContext(ctx, `INSERT INTO wa_chats (jid) VALUES ($1) ON CONFLICT DO NOTHING`)
		return err
	}
	for rows.Next() {
		var id, chat, sender, content, ts, fromMe, mtype, fname, u, mkey, fsha, fenc, flen any
		if err := rows.Scan(&id, &chat, &sender, &content, &ts, &fromMe, &mtype, &fname, &u, &mkey, &fsha, &fenc, &flen); err != nil {
			tx.Rollback()
			return nil, err
		}
		res.MessagesRead++
		t, err := toTime(ts)
		if err != nil || t == nil {
			res.MessagesSkipped++
			continue // a message without a usable date cannot be shown in order
		}
		filename := str(fname)
		if filename != "" {
			filename = wa.SafeFilename(filename, "document") // old rows were never cleaned
		}
		if _, err := ensureChat.ExecContext(ctx, str(chat)); err != nil {
			tx.Rollback()
			return nil, err
		}
		r, err := stmt.ExecContext(ctx, str(id), str(chat), str(sender), str(content), *t, boolean(fromMe),
			nullIf(str(mtype)), nullIf(filename), nullIf(str(u)), blob(mkey), blob(fsha), blob(fenc), i64(flen))
		if err != nil {
			tx.Rollback()
			return nil, fmt.Errorf("message %v: %w", id, err)
		}
		if n, _ := r.RowsAffected(); n > 0 {
			res.MessagesImported++
		}
		if res.MessagesRead%batch == 0 {
			if err := commit(true); err != nil {
				return nil, err
			}
			if progress != nil {
				progress(res.MessagesRead, total)
			}
		}
	}
	if err := rows.Err(); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err := commit(false); err != nil {
		return nil, err
	}
	if progress != nil {
		progress(res.MessagesRead, total)
	}
	return res, nil
}

func nullIf(s string) any {
	if s == "" {
		return nil
	}
	return s
}
