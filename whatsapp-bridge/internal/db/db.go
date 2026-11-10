// Package db opens the PostgreSQL connection shared by the WhatsApp session
// store (whatsmeow) and the message/settings store, and keeps the
// application schema up to date.
package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
)

// Open connects to PostgreSQL and checks the connection, retrying for up to
// wait (the database service may still be starting when Windows boots).
func Open(ctx context.Context, url string, wait time.Duration) (*sql.DB, error) {
	d, err := sql.Open("pgx", url)
	if err != nil {
		return nil, err
	}
	d.SetMaxOpenConns(10)
	d.SetMaxIdleConns(4)
	d.SetConnMaxIdleTime(5 * time.Minute)
	deadline := time.Now().Add(wait)
	for {
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err = d.PingContext(pctx)
		cancel()
		if err == nil {
			return d, nil
		}
		if time.Now().After(deadline) {
			d.Close()
			return nil, fmt.Errorf("cannot reach PostgreSQL: %w", err)
		}
		select {
		case <-ctx.Done():
			d.Close()
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}

// migrations are applied in order, each once. Never edit an applied one;
// append a new entry instead.
var migrations = []string{
	// 1: messages and chats (same shape as the original SQLite schema, so the
	// history from the Mac can be imported as is).
	`CREATE TABLE IF NOT EXISTS wa_chats (
		jid               TEXT PRIMARY KEY,
		name              TEXT,
		last_message_time TIMESTAMPTZ
	);
	CREATE TABLE IF NOT EXISTS wa_messages (
		id              TEXT NOT NULL,
		chat_jid        TEXT NOT NULL REFERENCES wa_chats(jid) ON DELETE CASCADE,
		sender          TEXT,
		content         TEXT,
		timestamp       TIMESTAMPTZ,
		is_from_me      BOOLEAN NOT NULL DEFAULT FALSE,
		media_type      TEXT,
		filename        TEXT,
		url             TEXT,
		media_key       BYTEA,
		file_sha256     BYTEA,
		file_enc_sha256 BYTEA,
		file_length     BIGINT,
		PRIMARY KEY (id, chat_jid)
	);
	CREATE INDEX IF NOT EXISTS wa_messages_chat_ts ON wa_messages (chat_jid, timestamp DESC);
	CREATE INDEX IF NOT EXISTS wa_messages_ts ON wa_messages (timestamp DESC);
	CREATE INDEX IF NOT EXISTS wa_messages_sender ON wa_messages (sender);
	CREATE INDEX IF NOT EXISTS wa_messages_id ON wa_messages (id);
	CREATE INDEX IF NOT EXISTS wa_chats_last ON wa_chats (last_message_time DESC);`,

	// 2: runtime settings, approved recipients and the audit log.
	`CREATE TABLE IF NOT EXISTS app_settings (
		key        TEXT PRIMARY KEY,
		value      JSONB NOT NULL,
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS approved_recipients (
		jid      TEXT PRIMARY KEY,
		kind     TEXT NOT NULL CHECK (kind IN ('contact','group')),
		label    TEXT NOT NULL DEFAULT '',
		added_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		added_by TEXT NOT NULL
	);
	CREATE TABLE IF NOT EXISTS audit_log (
		id      BIGSERIAL PRIMARY KEY,
		at      TIMESTAMPTZ NOT NULL DEFAULT now(),
		actor   TEXT NOT NULL,
		action  TEXT NOT NULL,
		target  TEXT NOT NULL DEFAULT '',
		outcome TEXT NOT NULL DEFAULT 'ok',
		details JSONB NOT NULL DEFAULT '{}'::jsonb
	);
	CREATE INDEX IF NOT EXISTS audit_log_at ON audit_log (at DESC);
	CREATE INDEX IF NOT EXISTS audit_log_action ON audit_log (action, at DESC);`,

	// 3: one-time links that open the web panel (made by "whatsapp-mcp panel").
	`CREATE TABLE IF NOT EXISTS panel_login (
		token_hash TEXT PRIMARY KEY,
		expires_at TIMESTAMPTZ NOT NULL,
		used_at    TIMESTAMPTZ
	);`,

	// 4: what the owner knows about each chat: category and country (the
	// country is where the person is, which the phone prefix only suggests).
	`CREATE TABLE IF NOT EXISTS chat_meta (
		jid        TEXT PRIMARY KEY,
		category   TEXT NOT NULL DEFAULT '',
		country    TEXT NOT NULL DEFAULT '',
		updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
		updated_by TEXT NOT NULL DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS chat_meta_category ON chat_meta (category);
	CREATE INDEX IF NOT EXISTS chat_meta_country ON chat_meta (country);`,
}

// Migrate applies pending application migrations inside transactions.
func Migrate(ctx context.Context, d *sql.DB) error {
	if _, err := d.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS app_schema_version (
		version    INT PRIMARY KEY,
		applied_at TIMESTAMPTZ NOT NULL DEFAULT now())`); err != nil {
		return err
	}
	// Serialize concurrent upgrades (installer + service starting).
	conn, err := d.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock(726352)`); err != nil {
		return err
	}
	defer conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock(726352)`)

	var current int
	if err := conn.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM app_schema_version`).Scan(&current); err != nil {
		return err
	}
	for i := current; i < len(migrations); i++ {
		tx, err := conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[i]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d: %w", i+1, err)
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO app_schema_version (version) VALUES ($1)`, i+1); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}
