package wa

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/types"
)

// RequestHistory asks the linked phone to send again the last count
// messages of a chat (WhatsApp "history on demand"). The request goes only
// to the owner's own phone, never to the contact. The answer arrives a bit
// later as a normal history sync and fills in messages that are missing.
func (b *Bridge) RequestHistory(ctx context.Context, chatJID string, count int, actor string) error {
	if !b.Client.IsConnected() || b.Client.Store.ID == nil {
		return errNotConnected
	}
	if count <= 0 || count > 200 {
		count = 100
	}
	chat, err := types.ParseJID(chatJID)
	if err != nil {
		return fmt.Errorf("invalid chat id: %w", err)
	}
	var id string
	var ts time.Time
	var fromMe bool
	err = b.Store.DB.QueryRowContext(ctx, `SELECT id, timestamp, is_from_me FROM wa_messages
		WHERE chat_jid = $1 ORDER BY timestamp DESC LIMIT 1`, chatJID).Scan(&id, &ts, &fromMe)
	if errors.Is(err, sql.ErrNoRows) {
		return errors.New("there are no stored messages in this chat to start from")
	}
	if err != nil {
		return err
	}
	info := &types.MessageInfo{MessageSource: types.MessageSource{Chat: chat, IsFromMe: fromMe}, ID: id, Timestamp: ts}
	if _, err := b.Client.SendPeerMessage(ctx, b.Client.BuildHistorySyncRequest(info, count)); err != nil {
		b.Policy.Audit(ctx, actor, "history.request", chatJID, "error", map[string]any{"error": err.Error()})
		return err
	}
	b.Policy.Audit(ctx, actor, "history.request", chatJID, "ok", map[string]any{"count": count})
	return nil
}
