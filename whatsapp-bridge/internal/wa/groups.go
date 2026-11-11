package wa

import (
	"context"
	"errors"
	"sort"
	"strings"

	"go.mau.fi/whatsmeow/types"
)

// GroupSummary is one group the account belongs to.
type GroupSummary struct {
	JID     string `json:"jid"`
	Name    string `json:"name"`
	Members int    `json:"members"`
}

// Member is a group participant with what could be learnt about them.
type Member struct {
	Phone     string `json:"phone,omitempty"` // digits, empty when WhatsApp hides it
	LID       string `json:"lid,omitempty"`   // user part of the LID
	PhoneHint string `json:"phone_hint,omitempty"`
	Name      string `json:"name,omitempty"` // WhatsApp profile name as is
	IsAdmin   bool   `json:"is_admin,omitempty"`
}

var errNotConnected = errors.New("WhatsApp is not connected right now")

// JoinedGroups lists the groups of the account, by name.
func (b *Bridge) JoinedGroups(ctx context.Context) ([]GroupSummary, error) {
	if !b.Client.IsConnected() || b.Client.Store.ID == nil {
		return nil, errNotConnected
	}
	gs, err := b.Client.GetJoinedGroups(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]GroupSummary, 0, len(gs))
	for _, g := range gs {
		out = append(out, GroupSummary{JID: g.JID.String(), Name: g.GroupName.Name, Members: len(g.Participants)})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out, nil
}

// GroupMembers returns the members of a group with their real phone number
// where WhatsApp lets it be known: from the group itself, from WhatsApp's
// own LID→number map, or not at all (then only the LID and a hint such as
// "+57∙∙∙∙∙∙∙∙67"). The own account is left out.
func (b *Bridge) GroupMembers(ctx context.Context, groupJID string) (name string, members []Member, err error) {
	if !b.Client.IsConnected() || b.Client.Store.ID == nil {
		return "", nil, errNotConnected
	}
	jid, err := types.ParseJID(groupJID)
	if err != nil || jid.Server != types.GroupServer {
		return "", nil, errors.New("that is not a group id (it must end in @g.us)")
	}
	gi, err := b.Client.GetGroupInfo(ctx, jid)
	if err != nil {
		return "", nil, err
	}
	self := b.Client.Store.ID.User
	selfLID := b.Client.Store.LID.User
	for _, p := range gi.Participants {
		m := resolveMember(ctx, b, p)
		if (m.Phone != "" && m.Phone == self) || (m.LID != "" && m.LID == selfLID) {
			continue
		}
		if m.Phone == "" && m.LID == "" {
			continue
		}
		members = append(members, m)
	}
	return gi.GroupName.Name, members, nil
}

// lidResolver and contactLookup let the resolution logic be tested without
// a WhatsApp connection.
type lidResolver interface {
	pnForLID(ctx context.Context, lid types.JID) types.JID
	contact(ctx context.Context, jid types.JID) types.ContactInfo
}

func (b *Bridge) pnForLID(ctx context.Context, lid types.JID) types.JID {
	pn, err := b.Client.Store.LIDs.GetPNForLID(ctx, lid)
	if err != nil {
		return types.EmptyJID
	}
	return pn
}

func (b *Bridge) contact(ctx context.Context, jid types.JID) types.ContactInfo {
	c, err := b.Client.Store.Contacts.GetContact(ctx, jid)
	if err != nil {
		return types.ContactInfo{}
	}
	return c
}

func resolveMember(ctx context.Context, r lidResolver, p types.GroupParticipant) Member {
	m := Member{IsAdmin: p.IsAdmin || p.IsSuperAdmin}
	var pn, lid types.JID
	if p.PhoneNumber.Server == types.DefaultUserServer && p.PhoneNumber.User != "" {
		pn = p.PhoneNumber
	}
	if p.LID.Server == types.HiddenUserServer && p.LID.User != "" {
		lid = p.LID
	}
	switch p.JID.Server {
	case types.DefaultUserServer:
		if pn.IsEmpty() {
			pn = p.JID
		}
	case types.HiddenUserServer:
		if lid.IsEmpty() {
			lid = p.JID
		}
	}
	if pn.IsEmpty() && !lid.IsEmpty() {
		if x := r.pnForLID(ctx, lid.ToNonAD()); x.Server == types.DefaultUserServer && x.User != "" {
			pn = x
		}
	}
	m.Phone, m.LID = pn.User, lid.User
	var hint string
	for _, j := range []types.JID{pn, lid} {
		if j.IsEmpty() {
			continue
		}
		c := r.contact(ctx, j.ToNonAD())
		if m.Name == "" {
			for _, n := range []string{c.PushName, c.FullName, c.BusinessName, c.FirstName} {
				if n = strings.TrimSpace(n); n != "" {
					m.Name = n
					break
				}
			}
		}
		if hint == "" {
			hint = c.RedactedPhone
		}
	}
	if m.Phone == "" {
		if hint == "" {
			hint = p.DisplayName
		}
		m.PhoneHint = hint
	}
	return m
}
