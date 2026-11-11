package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Transcript is a stored voice-note transcription.
type Transcript struct {
	Text      string    `json:"text"`
	Language  string    `json:"language"`
	Model     string    `json:"model"`
	Device    string    `json:"device"`
	Seconds   float64   `json:"duration_seconds"`
	CreatedAt time.Time `json:"created_at"`
}

// GetTranscript returns a saved transcription, or nil.
func (s *Store) GetTranscript(ctx context.Context, messageID, chatJID string) (*Transcript, error) {
	var t Transcript
	err := s.DB.QueryRowContext(ctx, `SELECT text, language, model, device, seconds, created_at FROM media_transcripts
		WHERE message_id = $1 AND chat_jid = $2`, messageID, chatJID).Scan(&t.Text, &t.Language, &t.Model, &t.Device, &t.Seconds, &t.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// SaveTranscript stores a transcription so it is done only once.
func (s *Store) SaveTranscript(ctx context.Context, messageID, chatJID string, t Transcript) error {
	_, err := s.DB.ExecContext(ctx, `INSERT INTO media_transcripts (message_id, chat_jid, text, language, model, device, seconds)
		VALUES ($1,$2,$3,$4,$5,$6,$7) ON CONFLICT (message_id, chat_jid) DO UPDATE SET text = EXCLUDED.text,
		language = EXCLUDED.language, model = EXCLUDED.model, device = EXCLUDED.device, seconds = EXCLUDED.seconds, created_at = now()`,
		messageID, chatJID, t.Text, t.Language, t.Model, t.Device, t.Seconds)
	return err
}
