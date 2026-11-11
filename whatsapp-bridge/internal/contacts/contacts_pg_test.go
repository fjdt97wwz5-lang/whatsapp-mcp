package contacts

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// These tests need an empty Postgres database: WMCP_TEST_DB=postgres://...
// They are skipped otherwise.

type fakeGroups map[string][]wa.Member

func (f fakeGroups) GroupMembers(_ context.Context, g string) (string, []wa.Member, error) {
	ms, ok := f[g]
	if !ok {
		return "", nil, fmt.Errorf("not in group")
	}
	return "Grupo " + g[:3], ms, nil
}

func testDB(t *testing.T) *sql.DB {
	url := os.Getenv("WMCP_TEST_DB")
	if url == "" {
		t.Skip("WMCP_TEST_DB not set")
	}
	ctx := context.Background()
	d, err := db.Open(ctx, url, 5*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(ctx, d); err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"DELETE FROM wa_contact_groups", "DELETE FROM wa_contacts", "DELETE FROM contact_source_groups", "DELETE FROM approved_recipients"} {
		if _, err := d.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { d.Close() })
	return d
}

func TestImportFlow(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	st, pol := mstore.New(d), policy.New(d)
	g1, g2 := "111@g.us", "222@g.us"
	src := fakeGroups{
		g1: {
			{Phone: "573001234567", LID: "9001", Name: "juan pérez de la cruz"},
			{LID: "9002", PhoneHint: "+1∙∙∙∙∙∙55", Name: "ANA"},
		},
		g2: {
			{Phone: "573001234567", LID: "9001", Name: "juan pérez de la cruz"}, // same person, other group
			{Phone: "5215512345678", Name: "maría-josé lópez"},
		},
	}
	im := &Importer{Store: st, Policy: pol, Groups: src}

	if _, err := im.Import(ctx, "panel"); err == nil {
		t.Fatal("import with no groups picked should fail")
	}
	if _, err := st.SetSourceGroup(ctx, "bad", "x", true, "panel"); err == nil {
		t.Fatal("non-group JID accepted")
	}
	for _, g := range []string{g1, g2} {
		if _, err := st.SetSourceGroup(ctx, g, "", true, "panel"); err != nil {
			t.Fatal(err)
		}
	}
	sum, err := im.Import(ctx, "panel")
	if err != nil {
		t.Fatal(err)
	}
	if sum.New != 3 || sum.Updated != 1 || sum.NoPhone != 1 || sum.Approved != 3 {
		t.Fatalf("summary %+v", sum)
	}
	cs, total, err := st.ListContacts(ctx, mstore.ContactFilter{})
	if err != nil || total != 3 {
		t.Fatalf("list %d %v", total, err)
	}
	by := map[string]mstore.SavedContact{}
	for _, c := range cs {
		by[c.JID] = c
	}
	j := by["573001234567@s.whatsapp.net"]
	if j.Name != "Juan Pérez de la Cruz" || j.Country != "CO" || len(j.Groups) != 2 || !j.Approved {
		t.Fatalf("juan %+v", j)
	}
	if a := by["9002@lid"]; a.Name != "Ana" || a.Phone != "" || a.PhoneHint == "" {
		t.Fatalf("ana %+v", a)
	}
	if m := by["5215512345678@s.whatsapp.net"]; m.Name != "María-José López" || m.Country != "MX" {
		t.Fatalf("maria %+v", m)
	}

	// The owner edits Juan's name and country; a new import keeps them.
	n, c := "juan el jefe", "us"
	if _, _, err := st.UpdateContact(ctx, j.JID, &n, &c, "panel"); err != nil {
		t.Fatal(err)
	}
	// Ana's number becomes known: her LID row moves to the phone JID.
	src[g1][1].Phone = "14075550155"
	if _, _, err := pol.SetSetting(ctx, "group_contacts_to_approved", "false", "panel"); err != nil {
		t.Fatal(err)
	}
	sum, err = im.Import(ctx, "panel")
	if err != nil {
		t.Fatal(err)
	}
	if sum.New != 0 || sum.Approved != 0 || sum.AddToApproved {
		t.Fatalf("second summary %+v", sum)
	}
	cs, total, _ = st.ListContacts(ctx, mstore.ContactFilter{})
	if total != 3 {
		t.Fatalf("after second import total %d", total)
	}
	by = map[string]mstore.SavedContact{}
	for _, c := range cs {
		by[c.JID] = c
	}
	if j := by["573001234567@s.whatsapp.net"]; j.Name != "Juan El Jefe" || j.Country != "US" {
		t.Fatalf("edit lost %+v", j)
	}
	a, ok := by["14075550155@s.whatsapp.net"]
	if !ok || a.LID != "9002" || a.Country != "US" {
		t.Fatalf("ana not moved to phone: %+v", by)
	}
	// Ana was approved under her LID; the flag still shows.
	if !a.Approved {
		t.Fatalf("ana lost approved flag")
	}
	// Filters.
	if _, n, _ := st.ListContacts(ctx, mstore.ContactFilter{Group: g2}); n != 2 {
		t.Fatalf("group filter %d", n)
	}
	if _, n, _ := st.ListContacts(ctx, mstore.ContactFilter{Country: "MX"}); n != 1 {
		t.Fatalf("country filter %d", n)
	}
	if _, n, _ := st.ListContacts(ctx, mstore.ContactFilter{Query: "lópez"}); n != 1 {
		t.Fatalf("query filter %d", n)
	}
	if fc, err := st.FindContact(ctx, "9002"); err != nil || fc.JID != a.JID {
		t.Fatalf("find by lid %v %v", fc, err)
	}
	if ok, _ := st.DeleteContact(ctx, a.JID); !ok {
		t.Fatal("delete")
	}
	// A group the account left is reported, not fatal.
	if _, err := st.SetSourceGroup(ctx, "333@g.us", "Viejo", true, "panel"); err != nil {
		t.Fatal(err)
	}
	sum, err = im.Import(ctx, "panel")
	if err != nil {
		t.Fatal(err)
	}
	var sawErr bool
	for _, g := range sum.Groups {
		if g.JID == "333@g.us" && g.Error != "" {
			sawErr = true
		}
	}
	if !sawErr {
		t.Fatalf("left group not reported: %+v", sum.Groups)
	}
	_, _, _ = pol.SetSetting(ctx, "group_contacts_to_approved", "true", "panel")
}

func TestChatAliases(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	st := mstore.New(d)
	if _, err := d.Exec(`INSERT INTO whatsmeow_lid_map(lid, pn) VALUES ('188660783808626','573009998877') ON CONFLICT DO NOTHING`); err != nil {
		t.Skip("lid map table not usable here: ", err)
	}
	got := st.ChatAliases(ctx, "188660783808626@lid")
	if len(got) != 2 || got[1] != "573009998877@s.whatsapp.net" {
		t.Fatalf("lid aliases %v", got)
	}
	got = st.ChatAliases(ctx, "573009998877@s.whatsapp.net")
	if len(got) != 2 || got[1] != "188660783808626@lid" {
		t.Fatalf("pn aliases %v", got)
	}
	if got := st.ChatAliases(ctx, "123@g.us"); len(got) != 1 {
		t.Fatalf("group aliases %v", got)
	}
	d.Exec(`DELETE FROM whatsmeow_lid_map WHERE lid='188660783808626'`)
}

// The price-list case: a photo stored under the LID chat, a later copy of
// the same message without its attachment, and a read by the phone chat.
func TestMediaKeptAndFoundByAlias(t *testing.T) {
	d := testDB(t)
	ctx := context.Background()
	st := mstore.New(d)
	d.Exec(`INSERT INTO whatsmeow_lid_map(lid, pn) VALUES ('188660783808626','573009998877') ON CONFLICT DO NOTHING`)
	defer d.Exec(`DELETE FROM whatsmeow_lid_map WHERE lid='188660783808626'`)
	d.Exec(`DELETE FROM wa_messages WHERE id='PRICE1'`)
	lidChat := "188660783808626@lid"
	if err := st.StoreChat(ctx, lidChat, "Mauricio", time.Now()); err != nil {
		t.Fatal(err)
	}
	m := mstore.Message{ID: "PRICE1", ChatJID: lidChat, Sender: "188660783808626", Content: "lista de precios", Timestamp: time.Now()}
	full := mstore.MediaInfo{MediaType: "image", Filename: "image_PRICE1.jpg", DirectPath: "/v/t62/abc.enc", MediaKey: []byte{1}, FileSHA256: []byte{2}, FileEncSHA256: []byte{3}, FileLength: 1234, MimeType: "image/jpeg"}
	if err := st.StoreMessage(ctx, m, full); err != nil {
		t.Fatal(err)
	}
	m.Content = ""
	if err := st.StoreMessage(ctx, m, mstore.MediaInfo{MediaType: "image"}); err != nil {
		t.Fatal(err)
	}
	mi, err := st.GetMediaInfo(ctx, "PRICE1", "573009998877@s.whatsapp.net")
	if err != nil {
		t.Fatal(err)
	}
	if mi.DirectPath != full.DirectPath || len(mi.MediaKey) != 1 || mi.FileLength != 1234 || mi.MimeType != "image/jpeg" {
		t.Fatalf("media lost: %+v", mi)
	}
	ms, err := st.ListMessages(ctx, mstore.ListFilter{ChatJID: "573009998877@s.whatsapp.net", Limit: 10})
	if err != nil || len(ms) != 1 || ms[0].Content != "lista de precios" || ms[0].MediaType != "image" {
		t.Fatalf("list by phone chat: %+v %v", ms, err)
	}
}
