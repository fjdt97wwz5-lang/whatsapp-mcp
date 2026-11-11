package wa

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"go.mau.fi/whatsmeow"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
)

type downloadable struct {
	directPath                    string
	mediaKey, fileSHA, fileEncSHA []byte
	length                        uint64
	mediaType                     whatsmeow.MediaType
}

func (d *downloadable) GetDirectPath() string             { return d.directPath }
func (d *downloadable) GetMediaKey() []byte               { return d.mediaKey }
func (d *downloadable) GetFileSHA256() []byte             { return d.fileSHA }
func (d *downloadable) GetFileEncSHA256() []byte          { return d.fileEncSHA }
func (d *downloadable) GetFileLength() uint64             { return d.length }
func (d *downloadable) GetMediaType() whatsmeow.MediaType { return d.mediaType }

// directPathFromURL turns https://mmg.whatsapp.net/v/t62...enc?ccb=... into /v/t62...enc
func directPathFromURL(u string) string {
	parts := strings.SplitN(u, ".net/", 2)
	if len(parts) < 2 {
		return u
	}
	return "/" + strings.SplitN(parts[1], "?", 2)[0]
}

// Downloaded describes a saved attachment.
type Downloaded struct {
	MediaType string `json:"media_type"`
	Filename  string `json:"filename"`
	Path      string `json:"path"`
	Cached    bool   `json:"cached"`
	MimeType  string `json:"mime_type,omitempty"`
	Bytes     int64  `json:"bytes,omitempty"`
}

// MediaInfo returns the stored attachment data of a message.
func (b *Bridge) MediaInfo(ctx context.Context, messageID, chatJID string) (mstore.MediaInfo, error) {
	return b.Store.GetMediaInfo(ctx, messageID, chatJID)
}

// Download saves the attachment of a message into the media folder and
// returns its path. The path is always built from cleaned names and
// checked to stay inside the media folder.
func (b *Bridge) Download(ctx context.Context, messageID, chatJID, actor string) (*Downloaded, error) {
	s, err := b.Policy.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !s.DownloadMediaEnabled {
		return nil, &policy.ErrDenied{Reason: "downloads are turned off (download_media_enabled = false)"}
	}
	return b.fetch(ctx, messageID, chatJID, actor, 0)
}

// FetchForReading saves an attachment so Claude can read it. It follows
// read_media_enabled and read_media_max_mb instead of the download switch.
func (b *Bridge) FetchForReading(ctx context.Context, messageID, chatJID, actor string) (*Downloaded, error) {
	s, err := b.Policy.Settings(ctx)
	if err != nil {
		return nil, err
	}
	if !s.ReadMediaEnabled {
		return nil, &policy.ErrDenied{Reason: "reading attachments is turned off (read_media_enabled = false)"}
	}
	return b.fetch(ctx, messageID, chatJID, actor, int64(s.ReadMediaMaxMB)<<20)
}

func (b *Bridge) fetch(ctx context.Context, messageID, chatJID, actor string, maxBytes int64) (*Downloaded, error) {
	mi, err := b.Store.GetMediaInfo(ctx, messageID, chatJID)
	if err != nil {
		return nil, err
	}
	if maxBytes > 0 && int64(mi.FileLength) > maxBytes {
		return nil, &policy.ErrDenied{Reason: fmt.Sprintf("the attachment is %.1f MB, more than read_media_max_mb (%d MB)", float64(mi.FileLength)/(1<<20), maxBytes>>20)}
	}
	if mi.MediaType == "" {
		return nil, fmt.Errorf("message %s has no attachment", messageID)
	}
	base, err := filepath.Abs(b.MediaDir)
	if err != nil {
		return nil, err
	}
	dir := filepath.Join(base, SafeDirName(chatJID))
	name := SafeFilename(SafeFilename(messageID, "msg")+"_"+SafeFilename(mi.Filename, mi.MediaType), mi.MediaType)
	path := filepath.Join(dir, name)
	if !policy.Within(base, path) {
		return nil, fmt.Errorf("refusing unsafe path for %q", mi.Filename)
	}
	if st, err := os.Stat(path); err == nil && st.Mode().IsRegular() {
		return &Downloaded{MediaType: mi.MediaType, Filename: name, Path: path, Cached: true, MimeType: mi.MimeType, Bytes: st.Size()}, nil
	}
	if !b.Client.IsConnected() {
		return nil, fmt.Errorf("not connected to WhatsApp")
	}
	directPath := mi.DirectPath
	if directPath == "" && mi.URL != "" {
		directPath = directPathFromURL(mi.URL)
	}
	if directPath == "" || len(mi.MediaKey) == 0 || len(mi.FileSHA256) == 0 || len(mi.FileEncSHA256) == 0 {
		return nil, fmt.Errorf("incomplete media information for download (WhatsApp did not send the download data for this message)")
	}
	var mt whatsmeow.MediaType
	switch mi.MediaType {
	case "image":
		mt = whatsmeow.MediaImage
	case "video":
		mt = whatsmeow.MediaVideo
	case "audio":
		mt = whatsmeow.MediaAudio
	case "document":
		mt = whatsmeow.MediaDocument
	case "sticker":
		mt = whatsmeow.MediaImage
	default:
		return nil, fmt.Errorf("unsupported media type %s", mi.MediaType)
	}
	data, err := b.Client.Download(ctx, &downloadable{
		directPath: directPath, mediaKey: mi.MediaKey, fileSHA: mi.FileSHA256,
		fileEncSHA: mi.FileEncSHA256, length: mi.FileLength, mediaType: mt,
	})
	if err != nil {
		return nil, fmt.Errorf("download failed: %w", err)
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// O_EXCL: never overwrite or follow an existing file.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		os.Remove(path)
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	b.Policy.Audit(ctx, actor, "media.download", chatJID, "ok", map[string]any{"message_id": messageID, "bytes": len(data), "file": name})
	return &Downloaded{MediaType: mi.MediaType, Filename: name, Path: path, MimeType: mi.MimeType, Bytes: int64(len(data))}, nil
}
