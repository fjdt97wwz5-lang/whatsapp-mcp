package panel

import (
	"context"
	"net/http"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// ---- writing style -----------------------------------------------------------

func (p *Panel) styleGet(ctx context.Context, w http.ResponseWriter) {
	if p.d.Style == nil {
		fail(w, 400, "no disponible")
		return
	}
	st, err := p.d.Style.Get(ctx, 50)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	s, _ := p.d.Policy.Settings(ctx)
	writeJSON(w, 200, map[string]any{"ok": true, "style": st, "enabled": s.WritingStyleEnabled, "relearn_days": s.WritingStyleDays})
}

func (p *Panel) styleLearn(ctx context.Context, w http.ResponseWriter) {
	if p.d.Style == nil {
		fail(w, 400, "no disponible")
		return
	}
	pr, err := p.d.Style.Learn(ctx, policy.ActorPanel)
	if err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "style.learn", "", "error", map[string]any{"error": err.Error()})
		fail(w, 500, err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "style.learn", "", "ok", map[string]any{"messages": pr.MessagesUsed, "chats": pr.Chats})
	p.styleGet(ctx, w)
}

func (p *Panel) styleNotes(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		Notes string `json:"notes"`
	}
	if err := decode(r, &body); err != nil || p.d.Style == nil {
		fail(w, 400, "datos no válidos")
		return
	}
	old, now, err := p.d.Style.SetNotes(ctx, body.Notes, false, policy.ActorPanel)
	if err != nil {
		fail(w, 400, err.Error())
		return
	}
	if old != now {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "style.notes", "", "ok", map[string]any{"old": old, "new": now})
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (p *Panel) styleCorrectionDelete(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID int64 `json:"id"`
	}
	if err := decode(r, &body); err != nil || p.d.Style == nil {
		fail(w, 400, "datos no válidos")
		return
	}
	ok, err := p.d.Style.DeleteCorrection(ctx, body.ID)
	if err != nil {
		fail(w, 500, err.Error())
		return
	}
	if ok {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "style.correction_delete", "", "ok", map[string]any{"id": body.ID})
	}
	writeJSON(w, 200, map[string]any{"ok": true, "deleted": ok})
}
