package wa

import (
	"context"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// SendRequest is one outgoing message.
type SendRequest struct {
	Recipient string // phone number or JID
	Text      string
	MediaPath string
	VoiceNote bool // convert/send the file as a voice note
}

// SendResult describes what happened.
type SendResult struct {
	Recipient string    `json:"recipient"`
	MessageID string    `json:"message_id,omitempty"`
	Timestamp time.Time `json:"timestamp,omitempty"`
}

// resolve turns user input into the JID to send to plus every alias under
// which the recipient may be on the approved list (phone JID and LID).
func (b *Bridge) resolve(ctx context.Context, recipient string) (types.JID, []string, error) {
	canon, _, err := policy.NormalizeRecipient(recipient)
	if err != nil {
		return types.JID{}, nil, err
	}
	jid, err := types.ParseJID(canon)
	if err != nil {
		return types.JID{}, nil, err
	}
	aliases := []string{canon}
	if b.Client != nil && b.Client.Store != nil && b.Client.Store.LIDs != nil {
		switch jid.Server {
		case types.DefaultUserServer:
			if lid, err := b.Client.Store.LIDs.GetLIDForPN(ctx, jid); err == nil && !lid.IsEmpty() {
				aliases = append(aliases, lid.ToNonAD().String())
			}
		case types.HiddenUserServer:
			if pn, err := b.Client.Store.LIDs.GetPNForLID(ctx, jid); err == nil && !pn.IsEmpty() {
				aliases = append(aliases, pn.ToNonAD().String())
				jid = pn.ToNonAD()
			}
		}
	}
	return jid, aliases, nil
}

// Send checks the policy, then sends. Every attempt is audited.
func (b *Bridge) Send(ctx context.Context, req SendRequest, actor string) (*SendResult, error) {
	s, err := b.Policy.Settings(ctx)
	if err != nil {
		return nil, err
	}
	details := map[string]any{"text_length": len([]rune(req.Text))}
	if s.AuditMessageText {
		details["text"] = req.Text
	}
	if req.MediaPath != "" {
		details["file"] = req.MediaPath
	}
	fail := func(target string, err error) (*SendResult, error) {
		outcome := "error"
		var d *policy.ErrDenied
		if errors.As(err, &d) {
			outcome = "denied"
		}
		details["error"] = err.Error()
		b.Policy.Audit(ctx, actor, "message.send", target, outcome, details)
		return nil, err
	}
	if strings.TrimSpace(req.Text) == "" && req.MediaPath == "" {
		return nil, errors.New("message text or file is required")
	}
	jid, aliases, err := b.resolve(ctx, req.Recipient)
	if err != nil {
		return fail(req.Recipient, err)
	}
	target := aliases[0]
	if err := b.Policy.CheckSend(ctx, aliases); err != nil {
		return fail(target, err)
	}
	var path string
	if req.MediaPath != "" {
		if path, err = b.Policy.CheckAttachment(ctx, req.MediaPath); err != nil {
			return fail(target, err)
		}
	}
	if !b.Client.IsConnected() || !b.Client.IsLoggedIn() {
		return fail(target, errors.New("not connected to WhatsApp"))
	}
	if err := b.Policy.TakeSendSlot(ctx); err != nil {
		return fail(target, err)
	}
	msg := &waE2E.Message{}
	if path != "" {
		if req.VoiceNote && !strings.EqualFold(filepath.Ext(path), ".ogg") {
			conv, err := ConvertToOpus(ctx, b.FFmpeg, path)
			if err != nil {
				return fail(target, err)
			}
			defer os.Remove(conv)
			path = conv
		}
		msg, err = b.buildMedia(ctx, path, req.Text, req.VoiceNote)
		if err != nil {
			return fail(target, err)
		}
	} else {
		msg.Conversation = proto.String(req.Text)
	}
	resp, err := b.Client.SendMessage(ctx, jid, msg)
	if err != nil {
		return fail(target, fmt.Errorf("send failed: %w", err))
	}
	details["message_id"] = resp.ID
	b.Policy.Audit(ctx, actor, "message.send", target, "ok", details)
	return &SendResult{Recipient: target, MessageID: resp.ID, Timestamp: resp.Timestamp}, nil
}

func mediaKind(path string, data []byte) (whatsmeow.MediaType, string) {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".jpg", ".jpeg":
		return whatsmeow.MediaImage, "image/jpeg"
	case ".png":
		return whatsmeow.MediaImage, "image/png"
	case ".webp":
		return whatsmeow.MediaImage, "image/webp"
	case ".gif":
		return whatsmeow.MediaImage, "image/gif"
	case ".ogg", ".opus":
		return whatsmeow.MediaAudio, "audio/ogg; codecs=opus"
	case ".mp3":
		return whatsmeow.MediaAudio, "audio/mpeg"
	case ".m4a", ".aac":
		return whatsmeow.MediaAudio, "audio/mp4"
	case ".mp4":
		return whatsmeow.MediaVideo, "video/mp4"
	case ".3gp":
		return whatsmeow.MediaVideo, "video/3gpp"
	case ".mov":
		return whatsmeow.MediaVideo, "video/quicktime"
	}
	mt := mime.TypeByExtension(ext)
	if mt == "" {
		mt = http.DetectContentType(data)
	}
	if mt == "" {
		mt = "application/octet-stream"
	}
	return whatsmeow.MediaDocument, mt
}

func (b *Bridge) buildMedia(ctx context.Context, path, caption string, voice bool) (*waE2E.Message, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read file: %w", err)
	}
	kind, mimeType := mediaKind(path, data)
	if voice && kind != whatsmeow.MediaAudio {
		return nil, errors.New("voice notes must be audio files")
	}
	up, err := b.Client.Upload(ctx, data, kind)
	if err != nil {
		return nil, fmt.Errorf("upload failed: %w", err)
	}
	msg := &waE2E.Message{}
	switch kind {
	case whatsmeow.MediaImage:
		msg.ImageMessage = &waE2E.ImageMessage{Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: &up.FileLength}
	case whatsmeow.MediaVideo:
		msg.VideoMessage = &waE2E.VideoMessage{Caption: proto.String(caption), Mimetype: proto.String(mimeType),
			URL: &up.URL, DirectPath: &up.DirectPath, MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256,
			FileSHA256: up.FileSHA256, FileLength: &up.FileLength}
	case whatsmeow.MediaAudio:
		am := &waE2E.AudioMessage{Mimetype: proto.String(mimeType), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength}
		if strings.Contains(mimeType, "opus") {
			secs, wave, err := analyzeOggOpus(data)
			if err != nil {
				return nil, err
			}
			am.Seconds, am.Waveform, am.PTT = proto.Uint32(secs), wave, proto.Bool(true)
		}
		msg.AudioMessage = am
	default:
		name := SafeFilename(filepath.Base(path), "document")
		msg.DocumentMessage = &waE2E.DocumentMessage{Title: proto.String(name), FileName: proto.String(name),
			Caption: proto.String(caption), Mimetype: proto.String(mimeType), URL: &up.URL, DirectPath: &up.DirectPath,
			MediaKey: up.MediaKey, FileEncSHA256: up.FileEncSHA256, FileSHA256: up.FileSHA256, FileLength: &up.FileLength}
	}
	return msg, nil
}
