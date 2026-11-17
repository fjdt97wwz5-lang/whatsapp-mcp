package panel

import (
	"context"
	"net/http"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// ---- eBay sale alerts --------------------------------------------------------

func (p *Panel) ebayGet(ctx context.Context, w http.ResponseWriter) {
	if p.d.Ebay == nil {
		fail(w, 400, "no disponible")
		return
	}
	st := p.d.Ebay.Status(ctx)
	notice, reminder, _ := p.d.Ebay.Preview(ctx)
	writeJSON(w, 200, map[string]any{"ok": true, "status": st, "preview_notice": notice, "preview_reminder": reminder})
}

func (p *Panel) ebayConnect(ctx context.Context, w http.ResponseWriter, r *http.Request) {
	var body struct {
		ClientID string `json:"client_id"`
		Tenant   string `json:"tenant"`
	}
	if err := decode(r, &body); err != nil || p.d.Ebay == nil {
		fail(w, 400, "datos no válidos")
		return
	}
	if _, err := p.d.Ebay.Connect(ctx, body.ClientID, body.Tenant); err != nil {
		p.d.Policy.Audit(ctx, policy.ActorPanel, "ebay.connect", "", "error", map[string]any{"error": err.Error()})
		fail(w, 400, err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "ebay.connect_start", "", "ok", map[string]any{"tenant": body.Tenant})
	p.ebayGet(ctx, w)
}

func (p *Panel) ebayDisconnect(ctx context.Context, w http.ResponseWriter) {
	if p.d.Ebay == nil {
		fail(w, 400, "no disponible")
		return
	}
	if err := p.d.Ebay.Disconnect(ctx); err != nil {
		fail(w, 500, err.Error())
		return
	}
	p.d.Policy.Audit(ctx, policy.ActorPanel, "ebay.disconnect", "", "ok", nil)
	p.ebayGet(ctx, w)
}

func (p *Panel) ebayCheck(ctx context.Context, w http.ResponseWriter) {
	if p.d.Ebay == nil {
		fail(w, 400, "no disponible")
		return
	}
	p.d.Ebay.CheckNow()
	writeJSON(w, 200, map[string]any{"ok": true})
}
