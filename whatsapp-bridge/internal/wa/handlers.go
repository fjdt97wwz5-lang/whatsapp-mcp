package wa

import (
	"context"
	"fmt"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// extractText returns the text of a message (plain, extended, or captions).
func extractText(m *waE2E.Message) string {
	if m == nil {
		return ""
	}
	if t := m.GetConversation(); t != "" {
		return t
	}
	if e := m.GetExtendedTextMessage(); e != nil {
		return e.GetText()
	}
	if i := m.GetImageMessage(); i != nil {
		return i.GetCaption()
	}
	if v := m.GetVideoMessage(); v != nil {
		return v.GetCaption()
	}
	if d := m.GetDocumentMessage(); d != nil {
		return d.GetCaption()
	}
	return ""
}

// extractMedia returns what is needed to download an attachment later.
// File names coming from the sender are cleaned here, before storing.
func extractMedia(m *waE2E.Message, ts time.Time) mstore.MediaInfo {
	if m == nil {
		return mstore.MediaInfo{}
	}
	stamp := ts.Format("20060102_150405")
	if img := m.GetImageMessage(); img != nil {
		return mstore.MediaInfo{MediaType: "image", Filename: "image_" + stamp + ".jpg", URL: img.GetURL(),
			MediaKey: img.GetMediaKey(), FileSHA256: img.GetFileSHA256(), FileEncSHA256: img.GetFileEncSHA256(), FileLength: img.GetFileLength()}
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return mstore.MediaInfo{MediaType: "video", Filename: "video_" + stamp + ".mp4", URL: vid.GetURL(),
			MediaKey: vid.GetMediaKey(), FileSHA256: vid.GetFileSHA256(), FileEncSHA256: vid.GetFileEncSHA256(), FileLength: vid.GetFileLength()}
	}
	if aud := m.GetAudioMessage(); aud != nil {
		return mstore.MediaInfo{MediaType: "audio", Filename: "audio_" + stamp + ".ogg", URL: aud.GetURL(),
			MediaKey: aud.GetMediaKey(), FileSHA256: aud.GetFileSHA256(), FileEncSHA256: aud.GetFileEncSHA256(), FileLength: aud.GetFileLength()}
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		return mstore.MediaInfo{MediaType: "document", Filename: SafeFilename(doc.GetFileName(), "document_"+stamp), URL: doc.GetURL(),
			MediaKey: doc.GetMediaKey(), FileSHA256: doc.GetFileSHA256(), FileEncSHA256: doc.GetFileEncSHA256(), FileLength: doc.GetFileLength()}
	}
	return mstore.MediaInfo{}
}

func (b *Bridge) handleMessage(ctx context.Context, msg *events.Message) {
	chatJID := msg.Info.Chat.String()
	sender := msg.Info.Sender.User
	if msg.Info.IsFromMe && b.Client.Store.ID != nil {
		sender = b.Client.Store.ID.User
	}
	name := b.chatName(ctx, msg.Info.Chat, chatJID, "", msg.Info.PushName, sender)
	if err := b.Store.StoreChat(ctx, chatJID, name, msg.Info.Timestamp); err != nil {
		b.Log.Warnf("store chat: %v", err)
		return
	}
	content := extractText(msg.Message)
	mi := extractMedia(msg.Message, msg.Info.Timestamp)
	if content == "" && mi.MediaType == "" {
		return
	}
	err := b.Store.StoreMessage(ctx, mstore.Message{
		ID: msg.Info.ID, ChatJID: chatJID, Sender: sender, Content: content,
		Timestamp: msg.Info.Timestamp, IsFromMe: msg.Info.IsFromMe,
	}, mi)
	if err != nil {
		b.Log.Warnf("store message: %v", err)
		return
	}
	dir := "←"
	if msg.Info.IsFromMe {
		dir = "→"
	}
	// Message text is not written to the log on purpose (privacy).
	b.Log.Debugf("%s %s in %s (%d chars, media=%s)", dir, sender, chatJID, len(content), mi.MediaType)
}

// chatName works out a readable chat name. historyName is the name given by
// a history sync, pushName the sender's own display name.
func (b *Bridge) chatName(ctx context.Context, jid types.JID, chatJID, historyName, pushName, sender string) string {
	if n := b.Store.ChatName(ctx, chatJID); n != "" {
		return n
	}
	if jid.Server == types.GroupServer {
		if historyName != "" {
			return historyName
		}
		if gi, err := b.Client.GetGroupInfo(ctx, jid); err == nil && gi.Name != "" {
			return gi.Name
		}
		return fmt.Sprintf("Group %s", jid.User)
	}
	if historyName != "" {
		return historyName
	}
	if c, err := b.Client.Store.Contacts.GetContact(ctx, jid); err == nil {
		for _, n := range []string{c.FullName, c.BusinessName, c.PushName} {
			if n != "" {
				return n
			}
		}
	}
	if pushName != "" && jid.User == sender {
		return pushName
	}
	if sender != "" {
		return sender
	}
	return jid.User
}

func (b *Bridge) handleHistorySync(ctx context.Context, hs *events.HistorySync) {
	if hs.Data == nil {
		return
	}
	stored := 0
	for _, conv := range hs.Data.GetConversations() {
		chatJIDStr := conv.GetID()
		jid, err := types.ParseJID(chatJIDStr)
		if err != nil {
			continue
		}
		histName := conv.GetDisplayName()
		if histName == "" {
			histName = conv.GetName()
		}
		var latest time.Time
		for _, hm := range conv.GetMessages() {
			wm := hm.GetMessage()
			if wm == nil {
				continue
			}
			if t := time.Unix(int64(wm.GetMessageTimestamp()), 0); t.After(latest) {
				latest = t
			}
		}
		if latest.IsZero() {
			continue
		}
		name := b.chatName(ctx, jid, chatJIDStr, histName, "", "")
		if err := b.Store.StoreChat(ctx, chatJIDStr, name, latest); err != nil {
			b.Log.Warnf("history chat: %v", err)
			continue
		}
		for _, hm := range conv.GetMessages() {
			wm := hm.GetMessage()
			if wm == nil || wm.GetMessageTimestamp() == 0 {
				continue
			}
			evt, err := b.Client.ParseWebMessage(jid, wm)
			if err != nil || evt == nil {
				continue
			}
			content := extractText(evt.Message)
			mi := extractMedia(evt.Message, evt.Info.Timestamp)
			if content == "" && mi.MediaType == "" {
				continue
			}
			sender := evt.Info.Sender.User
			if evt.Info.IsFromMe && b.Client.Store.ID != nil {
				sender = b.Client.Store.ID.User
			}
			if err := b.Store.StoreMessage(ctx, mstore.Message{
				ID: evt.Info.ID, ChatJID: chatJIDStr, Sender: sender, Content: content,
				Timestamp: evt.Info.Timestamp, IsFromMe: evt.Info.IsFromMe,
			}, mi); err != nil {
				b.Log.Warnf("history message: %v", err)
				continue
			}
			stored++
		}
	}
	b.Log.Infof("History sync: stored %d messages from %d conversations", stored, len(hs.Data.GetConversations()))
}
