package panel

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
)

// "Contactos" section: groups to take contacts from, the import button,
// and the saved contacts.

type groupRow struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name"`
	Members         int        `json:"members"`
	Selected        bool       `json:"selected"`
	LastImportAt    *time.Time `json:"last_import_at,omitempty"`
	LastImportCount int        `json:"last_import_count"`
}

func (p *Panel) contactGroups(ctx context.Context, w http.ResponseWriter) {
	sel, err := p.d.Store.ListSourceGroups(ctx)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	picked := map[string]mstore.SourceGroup{}
	for _, g := range sel {
		picked[g.JID] = g
	}
	var rows []groupRow
	live := true
	gs, err := p.d.Bridge.JoinedGroups(ctx)
	if err != nil {
		// Not connected: show what the stored history knows.
		live = false
		known, _ := p.d.Store.GroupChats(ctx)
		for _, g := range known {
			rows = append(rows, groupRow{JID: g.JID, Name: g.Name, Members: -1})
		}
	} else {
		for _, g := range gs {
			rows = append(rows, groupRow{JID: g.JID, Name: g.Name, Members: g.Members})
		}
	}
	seen := map[string]bool{}
	for i := range rows {
		seen[rows[i].JID] = true
		if s, ok := picked[rows[i].JID]; ok {
			rows[i].Selected, rows[i].LastImportAt, rows[i].LastImportCount = true, s.LastImportAt, s.LastImportCount
			if rows[i].Name == "" {
				rows[i].Name = s.Name
			}
		}
	}
	for _, s := range sel { // picked groups the account left
		if !seen[s.JID] {
			rows = append(rows, groupRow{JID: s.JID, Name: s.Name, Members: -1, Selected: true, LastImportAt: s.LastImportAt, LastImportCount: s.LastImportCount})
		}
	}
	st, _ := p.d.Policy.Settings(ctx)
	writeJSON(w, 200, map[string]any{"ok": true, "groups": rows, "live": live, "add_to_approved": st.GroupContactsApproved})
}

func (p *Panel) contactGroupSet(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		JID      string `json:"jid"`
		Name     string `json:"name"`
		Selected bool   `json:"selected"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	changed, err := p.d.Store.SetSourceGroup(ctx, strings.TrimSpace(body.JID), body.Name, body.Selected, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if changed {
		action := "contacts.group_add"
		if !body.Selected {
			action = "contacts.group_remove"
		}
		p.d.Policy.Audit(ctx, policy.ActorPanel, action, body.JID, "ok", map[string]any{"name": body.Name})
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) contactImport(ctx context.Context, w http.ResponseWriter) {
	if p.d.Contacts == nil {
		fail(w, 500, "no disponible")
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	sum, err := p.d.Contacts.Import(ctx, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "summary": sum})
}

func (p *Panel) contactList(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	cs, total, err := p.d.Store.ListContacts(ctx, mstore.ContactFilter{
		Query: q.Get("q"), Group: q.Get("group"), Country: q.Get("country"), Limit: limit, Offset: offset,
	})
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if cs == nil {
		cs = []mstore.SavedContact{}
	}
	writeJSON(w, 200, map[string]any{"ok": true, "contacts": cs, "total": total})
}

func (p *Panel) contactEdit(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		JID     string  `json:"jid"`
		Name    *string `json:"name"`
		Country *string `json:"country"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	oldName, oldCountry, err := p.d.Store.UpdateContact(ctx, body.JID, body.Name, body.Country, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if det := mstore.EditDetails(oldName, oldCountry, body.Name, body.Country); len(det) > 0 {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "contact.edit", body.JID, "ok", det)
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) contactRemove(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		JID string `json:"jid"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	ok, err := p.d.Store.DeleteContact(ctx, body.JID)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	outcome := "ok"
	if !ok {
		outcome = "not_found"
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "contact.remove", body.JID, outcome, nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- transcription add-on --------------------------------------------------

func (p *Panel) transcribeState(w http.ResponseWriter) {
	if p.d.Transcriber == nil {
		writeJSON(w, 200, map[string]any{"ok": true, "state": map[string]any{"supported": false}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "state": p.d.Transcriber.Status()})
}

func (p *Panel) transcribeInstall(ctx context.Context, w http.ResponseWriter) {
	if p.d.Transcriber == nil {
		fail(w, 400, "no disponible")
		return
	}
	if err := p.d.Transcriber.Install(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "transcribe.install", "", "started", nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) transcribeRemove(ctx context.Context, w http.ResponseWriter) {
	if p.d.Transcriber == nil {
		fail(w, 400, "no disponible")
		return
	}
	if err := p.d.Transcriber.Remove(); err != nil {
		fail(w, 400, err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "transcribe.remove", "", "ok", nil)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// ---- ask the phone for a chat's latest messages again -----------------------

func (p *Panel) chatHistory(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		JID   string `json:"jid"`
		Count int    `json:"count"`
	}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err.Error())
		return
	}
	if err := p.d.Bridge.RequestHistory(ctx, body.JID, body.Count, policy.ActorPanel); err != nil {
		fail(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
