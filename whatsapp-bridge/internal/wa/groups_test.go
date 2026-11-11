package wa

import (
	"context"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"google.golang.org/protobuf/proto"
)

type fakeResolver struct {
	lid map[string]string
	c   map[string]types.ContactInfo
}

func (f fakeResolver) pnForLID(_ context.Context, lid types.JID) types.JID {
	if pn, ok := f.lid[lid.User]; ok {
		return types.NewJID(pn, types.DefaultUserServer)
	}
	return types.EmptyJID
}
func (f fakeResolver) contact(_ context.Context, j types.JID) types.ContactInfo {
	return f.c[j.String()]
}

func TestResolveMember(t *testing.T) {
	r := fakeResolver{
		lid: map[string]string{"111": "573001112233"},
		c: map[string]types.ContactInfo{
			"573001112233@s.whatsapp.net": {Found: true, PushName: "MARIA lopez"},
			"222@lid":                     {Found: true, PushName: "Pedro", RedactedPhone: "+57∙∙∙∙∙∙∙∙67"},
			"14075551234@s.whatsapp.net":  {Found: true, FullName: "Ana Ruiz"},
		},
	}
	ctx := context.Background()
	// LID member whose number WhatsApp knows.
	m := resolveMember(ctx, r, types.GroupParticipant{JID: types.NewJID("111", types.HiddenUserServer)})
	if m.Phone != "573001112233" || m.LID != "111" || m.Name != "MARIA lopez" || m.PhoneHint != "" {
		t.Fatalf("lid known: %+v", m)
	}
	// LID member with hidden number.
	m = resolveMember(ctx, r, types.GroupParticipant{JID: types.NewJID("222", types.HiddenUserServer)})
	if m.Phone != "" || m.LID != "222" || m.PhoneHint != "+57∙∙∙∙∙∙∙∙67" || m.Name != "Pedro" {
		t.Fatalf("lid hidden: %+v", m)
	}
	// Group gives the number directly.
	m = resolveMember(ctx, r, types.GroupParticipant{JID: types.NewJID("333", types.HiddenUserServer),
		PhoneNumber: types.NewJID("14075551234", types.DefaultUserServer), IsAdmin: true})
	if m.Phone != "14075551234" || m.LID != "333" || m.Name != "Ana Ruiz" || !m.IsAdmin {
		t.Fatalf("pn given: %+v", m)
	}
}

var testTime = time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC)

func TestUnwrapAlbumChild(t *testing.T) {
	img := &waE2E.ImageMessage{URL: proto.String(""), DirectPath: proto.String("/v/t62/abc.enc"), Mimetype: proto.String("image/jpeg"),
		Caption: proto.String("Lista de precios"), MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}}
	m := &waE2E.Message{AssociatedChildMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{ImageMessage: img}}}
	if got := extractText(m); got != "Lista de precios" {
		t.Fatalf("text %q", got)
	}
	mi := extractMedia(m, testTime)
	if mi.MediaType != "image" || mi.DirectPath != "/v/t62/abc.enc" || mi.MimeType != "image/jpeg" {
		t.Fatalf("media %+v", mi)
	}
	// Document inside a group mention inside a spoiler.
	doc := &waE2E.DocumentMessage{FileName: proto.String("precios.xlsx"), Mimetype: proto.String("application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")}
	m = &waE2E.Message{SpoilerMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{
		GroupMentionedMessage: &waE2E.FutureProofMessage{Message: &waE2E.Message{DocumentMessage: doc}}}}}
	if mi := extractMedia(m, testTime); mi.MediaType != "document" || mi.Filename != "precios.xlsx" {
		t.Fatalf("doc %+v", mi)
	}
	if mi := extractMedia(&waE2E.Message{StickerMessage: &waE2E.StickerMessage{}}, testTime); mi.MediaType != "sticker" {
		t.Fatalf("sticker %+v", mi)
	}
}
