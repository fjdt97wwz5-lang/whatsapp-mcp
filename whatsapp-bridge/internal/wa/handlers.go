package wa

import (
	"context"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// unwrap opens the wrappers WhatsApp puts around a message that whatsmeow
// does not open itself, such as the photos of an album
// (associatedChildMessage), group mentions, spoilers and forwards from bots.
// Without this, those photos and files were silently not stored.
func unwrap(m *waE2E.Message) *waE2E.Message {
	for i := 0; m != nil && i < 8; i++ {
		var next *waE2E.Message
		for _, fp := range []*waE2E.FutureProofMessage{
			m.GetAssociatedChildMessage(), m.GetGroupMentionedMessage(), m.GetSpoilerMessage(),
			m.GetBotForwardedMessage(), m.GetEphemeralMessage(), m.GetViewOnceMessage(),
			m.GetViewOnceMessageV2(), m.GetViewOnceMessageV2Extension(), m.GetDocumentWithCaptionMessage(),
			m.GetLottieStickerMessage(), m.GetBotInvokeMessage(), m.GetStatusMentionMessage(),
			m.GetGroupStatusMessage(), m.GetGroupStatusMessageV2(), m.GetAudioStickerMessage(),
		} {
			if inner := fp.GetMessage(); inner != nil {
				next = inner
				break
			}
		}
		if next == nil {
			if d := m.GetDeviceSentMessage().GetMessage(); d != nil {
				next = d
			}
		}
		if next == nil {
			return m
		}
		m = next
	}
	return m
}

// extractText returns the text of a message (plain, extended, or captions).
func extractText(m *waE2E.Message) string {
	m = unwrap(m)
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
	if v := m.GetPtvMessage(); v != nil {
		return v.GetCaption()
	}
	if d := m.GetDocumentMessage(); d != nil {
		return d.GetCaption()
	}
	return ""
}

// mediaFields is what every downloadable WhatsApp attachment carries.
type mediaFields interface {
	GetURL() string
	GetDirectPath() string
	GetMediaKey() []byte
	GetFileSHA256() []byte
	GetFileEncSHA256() []byte
	GetFileLength() uint64
	GetMimetype() string
}

func mediaInfo(kind, filename string, f mediaFields) mstore.MediaInfo {
	return mstore.MediaInfo{MediaType: kind, Filename: filename, URL: f.GetURL(), DirectPath: f.GetDirectPath(),
		MediaKey: f.GetMediaKey(), FileSHA256: f.GetFileSHA256(), FileEncSHA256: f.GetFileEncSHA256(),
		FileLength: f.GetFileLength(), MimeType: f.GetMimetype()}
}

// extractMedia returns what is needed to download an attachment later.
// File names coming from the sender are cleaned here, before storing.
func extractMedia(m *waE2E.Message, ts time.Time) mstore.MediaInfo {
	m = unwrap(m)
	if m == nil {
		return mstore.MediaInfo{}
	}
	stamp := ts.Format("20060102_150405")
	if img := m.GetImageMessage(); img != nil {
		return mediaInfo("image", "image_"+stamp+extFor(img.GetMimetype(), ".jpg"), img)
	}
	if vid := m.GetVideoMessage(); vid != nil {
		return mediaInfo("video", "video_"+stamp+extFor(vid.GetMimetype(), ".mp4"), vid)
	}
	if vid := m.GetPtvMessage(); vid != nil {
		return mediaInfo("video", "videonote_"+stamp+".mp4", vid)
	}
	if aud := m.GetAudioMessage(); aud != nil {
		return mediaInfo("audio", "audio_"+stamp+extFor(aud.GetMimetype(), ".ogg"), aud)
	}
	if doc := m.GetDocumentMessage(); doc != nil {
		name := doc.GetFileName()
		if name == "" {
			name = doc.GetTitle()
		}
		if name == "" {
			name = "document_" + stamp + extFor(doc.GetMimetype(), "")
		}
		return mediaInfo("document", SafeFilename(name, "document_"+stamp), doc)
	}
	if st := m.GetStickerMessage(); st != nil {
		return mediaInfo("sticker", "sticker_"+stamp+".webp", st)
	}
	return mstore.MediaInfo{}
}

// extFor picks a file extension from a MIME type.
func extFor(mime, def string) string {
	mime = strings.ToLower(strings.TrimSpace(strings.SplitN(mime, ";", 2)[0]))
	switch mime {
	case "image/jpeg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "video/mp4":
		return ".mp4"
	case "audio/ogg":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/mp4", "audio/aac", "audio/x-m4a":
		return ".m4a"
	case "application/pdf":
		return ".pdf"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "text/csv":
		return ".csv"
	}
	return def
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
