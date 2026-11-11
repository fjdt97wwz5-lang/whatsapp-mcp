// Package contacts saves the members of the groups the owner picked as
// contacts, with their real phone number when WhatsApp lets it be known,
// and optionally adds them to the approved list. It runs only when the
// owner presses the button in the panel, never by itself.
package contacts

import (
	"context"
	"fmt"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
	mstore "github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/store"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// Source gives the members of a group (the WhatsApp bridge, or a fake in tests).
type Source interface {
	GroupMembers(ctx context.Context, groupJID string) (string, []wa.Member, error)
}

// Importer does the import.
type Importer struct {
	Store  *mstore.Store
	Policy *policy.Policy
	Groups Source
}

// GroupResult is the outcome for one group.
type GroupResult struct {
	JID      string `json:"jid"`
	Name     string `json:"name"`
	Members  int    `json:"members"`
	New      int    `json:"new"`
	Updated  int    `json:"updated"`
	NoPhone  int    `json:"no_phone"`
	Approved int    `json:"approved"`
	Error    string `json:"error,omitempty"`
}

// Summary is the outcome of one press of the button.
type Summary struct {
	Groups        []GroupResult `json:"groups"`
	New           int           `json:"new"`
	Updated       int           `json:"updated"`
	NoPhone       int           `json:"no_phone"`
	Approved      int           `json:"approved"`
	AddToApproved bool          `json:"add_to_approved"`
}

// Import saves the members of every picked group.
func (im *Importer) Import(ctx context.Context, actor string) (*Summary, error) {
	s, err := im.Policy.Settings(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := im.Store.ListSourceGroups(ctx)
	if err != nil {
		return nil, err
	}
	if len(groups) == 0 {
		return nil, fmt.Errorf("no groups chosen yet: tick the groups to take contacts from first")
	}
	sum := &Summary{AddToApproved: s.GroupContactsApproved}
	for _, g := range groups {
		gr := im.importGroup(ctx, g, s.GroupContactsApproved, actor)
		sum.Groups = append(sum.Groups, gr)
		sum.New += gr.New
		sum.Updated += gr.Updated
		sum.NoPhone += gr.NoPhone
		sum.Approved += gr.Approved
	}
	im.Policy.Audit(ctx, actor, "contacts.import", "", "ok", map[string]any{
		"groups": len(groups), "new": sum.New, "updated": sum.Updated, "no_phone": sum.NoPhone,
		"approved": sum.Approved, "add_to_approved": sum.AddToApproved,
	})
	return sum, nil
}

func (im *Importer) importGroup(ctx context.Context, g mstore.SourceGroup, approve bool, actor string) GroupResult {
	gr := GroupResult{JID: g.JID, Name: g.Name}
	name, members, err := im.Groups.GroupMembers(ctx, g.JID)
	if err != nil {
		gr.Error = err.Error()
		return gr
	}
	if name != "" {
		gr.Name = name
	}
	gr.Members = len(members)
	for _, m := range members {
		jid, created, err := im.Store.UpsertMember(ctx, mstore.MemberIn{
			Phone: m.Phone, LID: m.LID, PhoneHint: m.PhoneHint, Name: m.Name, GroupJID: g.JID, GroupName: gr.Name,
		}, actor)
		if err != nil {
			continue
		}
		if created {
			gr.New++
		} else {
			gr.Updated++
		}
		if m.Phone == "" {
			gr.NoPhone++
		}
		if !approve {
			continue
		}
		aliases := []string{jid}
		if m.Phone != "" {
			aliases = append(aliases, m.Phone+"@s.whatsapp.net")
		}
		if m.LID != "" {
			aliases = append(aliases, m.LID+"@lid")
		}
		if ok, err := im.Policy.IsApproved(ctx, aliases...); err != nil || ok {
			continue // already there: keep its label as the owner wrote it
		}
		label := mstore.TitleCase(m.Name)
		if label == "" {
			label = "Del grupo " + gr.Name
		}
		if _, err := im.Policy.AddApproved(ctx, jid, label, actor); err == nil {
			gr.Approved++
		}
	}
	_ = im.Store.MarkImported(ctx, g.JID, gr.Name, gr.Members)
	return gr
}
