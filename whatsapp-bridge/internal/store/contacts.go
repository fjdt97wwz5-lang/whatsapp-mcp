package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// SavedContact is a person saved from the members of a group.
type SavedContact struct {
	JID           string         `json:"jid"`
	Phone         string         `json:"phone,omitempty"`      // digits; empty when WhatsApp hides the number
	LID           string         `json:"lid,omitempty"`        // WhatsApp's private id for the person
	PhoneHint     string         `json:"phone_hint,omitempty"` // e.g. "+57∙∙∙∙∙∙∙∙67" when the number is hidden
	Name          string         `json:"name"`
	NameEdited    bool           `json:"name_edited"`
	Country       string         `json:"country,omitempty"`
	CountryEdited bool           `json:"country_edited"`
	Groups        []ContactGroup `json:"groups"`
	Approved      bool           `json:"approved"`
	CreatedAt     time.Time      `json:"created_at"`
	UpdatedAt     time.Time      `json:"updated_at"`
	UpdatedBy     string         `json:"updated_by"`
}

// ContactGroup is a group a contact was found in.
type ContactGroup struct {
	JID  string `json:"jid"`
	Name string `json:"name"`
}

// MemberIn is one group member to save.
type MemberIn struct {
	Phone     string // digits, may be empty
	LID       string // user part of the LID, may be empty
	PhoneHint string
	Name      string // WhatsApp profile name (formatted here)
	GroupJID  string
	GroupName string
}

// ContactJID is the key a member is saved under: the phone JID when the
// number is known, else the LID.
func (m MemberIn) ContactJID() string {
	if m.Phone != "" {
		return m.Phone + "@s.whatsapp.net"
	}
	if m.LID != "" {
		return m.LID + "@lid"
	}
	return ""
}

// UpsertMember saves a group member as a contact. A name or country the
// owner edited is never overwritten; when a contact first saved by its LID
// now shows its number, the row moves to the phone JID. created reports a
// new contact.
func (s *Store) UpsertMember(ctx context.Context, m MemberIn, by string) (jid string, created bool, err error) {
	m.Phone, m.LID = DigitsOnly(m.Phone), DigitsOnly(m.LID)
	jid = m.ContactJID()
	if jid == "" {
		return "", false, errors.New("member without phone or LID")
	}
	name := TitleCase(m.Name)
	country := ""
	if m.Phone != "" {
		country = SuggestCountry(m.Phone)
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback()
	// Find an existing row by phone JID or by LID.
	var cur string
	err = tx.QueryRowContext(ctx, `SELECT jid FROM wa_contacts
		WHERE jid = $1 OR ($2 <> '' AND lid = $2) OR ($3 <> '' AND phone = $3)
		ORDER BY (jid = $1) DESC LIMIT 1 FOR UPDATE`, jid, m.LID, m.Phone).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		created = true
		if _, err := tx.ExecContext(ctx, `INSERT INTO wa_contacts (jid, phone, lid, phone_hint, name, country, updated_by)
			VALUES ($1,$2,$3,$4,$5,$6,$7)`, jid, m.Phone, m.LID, m.PhoneHint, name, country, by); err != nil {
			return "", false, err
		}
	case err != nil:
		return "", false, err
	default:
		if cur != jid {
			// Number now visible (or row known by phone): move it to the new key.
			var taken bool
			_ = tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM wa_contacts WHERE jid = $1)`, jid).Scan(&taken)
			if taken {
				jid = cur
			} else if _, err := tx.ExecContext(ctx, `UPDATE wa_contacts SET jid = $1 WHERE jid = $2`, jid, cur); err != nil {
				return "", false, err
			}
		}
		if _, err := tx.ExecContext(ctx, `UPDATE wa_contacts SET
				phone = CASE WHEN $2 <> '' THEN $2 ELSE phone END,
				lid = CASE WHEN $3 <> '' THEN $3 ELSE lid END,
				phone_hint = CASE WHEN $4 <> '' THEN $4 ELSE phone_hint END,
				name = CASE WHEN NOT name_edited AND $5 <> '' THEN $5 ELSE name END,
				country = CASE WHEN NOT country_edited AND country = '' THEN $6 ELSE country END,
				updated_at = now(), updated_by = $7
			WHERE jid = $1`, jid, m.Phone, m.LID, m.PhoneHint, name, country, by); err != nil {
			return "", false, err
		}
	}
	if m.GroupJID != "" {
		if _, err := tx.ExecContext(ctx, `INSERT INTO wa_contact_groups (contact_jid, group_jid, group_name)
			VALUES ($1,$2,$3) ON CONFLICT (contact_jid, group_jid) DO UPDATE SET group_name = EXCLUDED.group_name`,
			jid, m.GroupJID, m.GroupName); err != nil {
			return "", false, err
		}
	}
	return jid, created, tx.Commit()
}

// ContactFilter selects saved contacts.
type ContactFilter struct {
	Query   string // name or number
	Group   string // group JID
	Country string // 2-letter code, "-" for none
	JID     string // exactly this contact
	Limit   int
	Offset  int
}

// ListContacts returns saved contacts by name, and how many match in total.
func (s *Store) ListContacts(ctx context.Context, f ContactFilter) ([]SavedContact, int, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 100
	}
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	if q := strings.TrimSpace(f.Query); q != "" {
		cond := "k.name ILIKE " + arg("%"+escapeLike(q)+"%")
		if d := DigitsOnly(q); len(d) >= 3 {
			cond += " OR k.phone LIKE " + arg("%"+d+"%")
		}
		where = append(where, "("+cond+")")
	}
	if f.Group != "" {
		where = append(where, "EXISTS (SELECT 1 FROM wa_contact_groups g WHERE g.contact_jid = k.jid AND g.group_jid = "+arg(f.Group)+")")
	}
	if f.JID != "" {
		where = append(where, "k.jid = "+arg(f.JID))
	}
	switch f.Country {
	case "":
	case "-":
		where = append(where, "k.country = ''")
	default:
		where = append(where, "k.country = "+arg(strings.ToUpper(f.Country)))
	}
	w := ""
	if len(where) > 0 {
		w = " WHERE " + strings.Join(where, " AND ")
	}
	var total int
	if err := s.DB.QueryRowContext(ctx, `SELECT count(*) FROM wa_contacts k`+w, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	q := `SELECT k.jid, k.phone, k.lid, k.phone_hint, k.name, k.name_edited, k.country, k.country_edited,
		k.created_at, k.updated_at, k.updated_by,
		EXISTS (SELECT 1 FROM approved_recipients a WHERE a.jid = k.jid
			OR (k.lid <> '' AND a.jid = k.lid || '@lid') OR (k.phone <> '' AND a.jid = k.phone || '@s.whatsapp.net'))
		FROM wa_contacts k` + w + ` ORDER BY lower(k.name), k.jid LIMIT ` + arg(f.Limit) + ` OFFSET ` + arg(f.Offset)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, 0, err
	}
	var out []SavedContact
	idx := map[string]int{}
	for rows.Next() {
		var c SavedContact
		if err := rows.Scan(&c.JID, &c.Phone, &c.LID, &c.PhoneHint, &c.Name, &c.NameEdited, &c.Country, &c.CountryEdited,
			&c.CreatedAt, &c.UpdatedAt, &c.UpdatedBy, &c.Approved); err != nil {
			rows.Close()
			return nil, 0, err
		}
		c.Groups = []ContactGroup{}
		idx[c.JID] = len(out)
		out = append(out, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	if len(out) > 0 {
		jids := make([]string, 0, len(out))
		for _, c := range out {
			jids = append(jids, c.JID)
		}
		gr, err := s.DB.QueryContext(ctx, `SELECT contact_jid, group_jid, group_name FROM wa_contact_groups
			WHERE contact_jid = ANY($1) ORDER BY group_name`, jids)
		if err != nil {
			return nil, 0, err
		}
		defer gr.Close()
		for gr.Next() {
			var cj string
			var g ContactGroup
			if err := gr.Scan(&cj, &g.JID, &g.Name); err != nil {
				return nil, 0, err
			}
			if i, ok := idx[cj]; ok {
				out[i].Groups = append(out[i].Groups, g)
			}
		}
		if err := gr.Err(); err != nil {
			return nil, 0, err
		}
	}
	return out, total, nil
}

// FindContact looks a contact up by JID, phone number or LID.
func (s *Store) FindContact(ctx context.Context, key string) (*SavedContact, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, errors.New("contact is required")
	}
	var jid string
	d := DigitsOnly(key)
	err := s.DB.QueryRowContext(ctx, `SELECT jid FROM wa_contacts WHERE jid = $1 OR ($2 <> '' AND (phone = $2 OR lid = $2)) LIMIT 1`,
		strings.ToLower(key), d).Scan(&jid)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("no saved contact %s", key)
	}
	if err != nil {
		return nil, err
	}
	cs, _, err := s.ListContacts(ctx, ContactFilter{JID: jid, Limit: 1})
	if err != nil {
		return nil, err
	}
	if len(cs) == 0 {
		return nil, fmt.Errorf("no saved contact %s", key)
	}
	return &cs[0], nil
}

// UpdateContact changes the name and/or country of a contact (nil = keep).
// An edited value is kept on later imports. It returns the old values.
func (s *Store) UpdateContact(ctx context.Context, jid string, name, country *string, by string) (oldName, oldCountry string, err error) {
	if name != nil {
		n := TitleCase(*name)
		if n == "" {
			return "", "", errors.New("the name cannot be empty")
		}
		if len([]rune(n)) > 120 {
			return "", "", errors.New("the name is too long")
		}
		name = &n
	}
	if country != nil {
		c := strings.ToUpper(strings.TrimSpace(*country))
		if c != "" && !countryRe.MatchString(c) {
			return "", "", fmt.Errorf("country must be a 2-letter ISO code like CO, US or ES")
		}
		country = &c
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	if err := tx.QueryRowContext(ctx, `SELECT name, country FROM wa_contacts WHERE jid = $1 FOR UPDATE`, jid).Scan(&oldName, &oldCountry); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", "", fmt.Errorf("no saved contact %s", jid)
		}
		return "", "", err
	}
	if name != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE wa_contacts SET name = $2, name_edited = true, updated_at = now(), updated_by = $3 WHERE jid = $1`, jid, *name, by); err != nil {
			return "", "", err
		}
	}
	if country != nil {
		if _, err := tx.ExecContext(ctx, `UPDATE wa_contacts SET country = $2, country_edited = true, updated_at = now(), updated_by = $3 WHERE jid = $1`, jid, *country, by); err != nil {
			return "", "", err
		}
	}
	return oldName, oldCountry, tx.Commit()
}

// DeleteContact removes a saved contact (not from the approved list).
func (s *Store) DeleteContact(ctx context.Context, jid string) (bool, error) {
	r, err := s.DB.ExecContext(ctx, `DELETE FROM wa_contacts WHERE jid = $1`, jid)
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// SourceGroup is a group the owner picked to take contacts from.
type SourceGroup struct {
	JID             string     `json:"jid"`
	Name            string     `json:"name"`
	SelectedAt      time.Time  `json:"selected_at"`
	SelectedBy      string     `json:"selected_by"`
	LastImportAt    *time.Time `json:"last_import_at,omitempty"`
	LastImportCount int        `json:"last_import_count"`
}

// ListSourceGroups returns the picked groups.
func (s *Store) ListSourceGroups(ctx context.Context) ([]SourceGroup, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT group_jid, name, selected_at, selected_by, last_import_at, last_import_count
		FROM contact_source_groups ORDER BY lower(name), group_jid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SourceGroup
	for rows.Next() {
		var g SourceGroup
		var last sql.NullTime
		if err := rows.Scan(&g.JID, &g.Name, &g.SelectedAt, &g.SelectedBy, &last, &g.LastImportCount); err != nil {
			return nil, err
		}
		if last.Valid {
			t := last.Time
			g.LastImportAt = &t
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

// SetSourceGroup picks or unpicks a group. It reports whether anything changed.
func (s *Store) SetSourceGroup(ctx context.Context, jid, name string, selected bool, by string) (bool, error) {
	if !strings.HasSuffix(jid, "@g.us") {
		return false, fmt.Errorf("%s is not a group", jid)
	}
	var r sql.Result
	var err error
	if selected {
		r, err = s.DB.ExecContext(ctx, `INSERT INTO contact_source_groups (group_jid, name, selected_by) VALUES ($1,$2,$3)
			ON CONFLICT (group_jid) DO UPDATE SET name = CASE WHEN EXCLUDED.name <> '' THEN EXCLUDED.name ELSE contact_source_groups.name END
			WHERE contact_source_groups.name IS DISTINCT FROM EXCLUDED.name AND EXCLUDED.name <> ''`, jid, strings.TrimSpace(name), by)
	} else {
		r, err = s.DB.ExecContext(ctx, `DELETE FROM contact_source_groups WHERE group_jid = $1`, jid)
	}
	if err != nil {
		return false, err
	}
	n, _ := r.RowsAffected()
	return n > 0, nil
}

// MarkImported records the result of an import of one group.
func (s *Store) MarkImported(ctx context.Context, jid, name string, count int) error {
	_, err := s.DB.ExecContext(ctx, `UPDATE contact_source_groups SET last_import_at = now(), last_import_count = $2,
		name = CASE WHEN $3 <> '' THEN $3 ELSE name END WHERE group_jid = $1`, jid, count, name)
	return err
}

// GroupChats lists the groups known from the stored history (used when
// WhatsApp is not connected).
func (s *Store) GroupChats(ctx context.Context) ([]ContactGroup, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT jid, COALESCE(name,'') FROM wa_chats WHERE jid LIKE '%@g.us' ORDER BY lower(COALESCE(name,'')), jid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ContactGroup
	for rows.Next() {
		var g ContactGroup
		if err := rows.Scan(&g.JID, &g.Name); err != nil {
			return nil, err
		}
		out = append(out, g)
	}
	return out, rows.Err()
}

var smallWords = map[string]bool{"de": true, "del": true, "la": true, "las": true, "los": true, "y": true, "e": true,
	"da": true, "do": true, "dos": true, "van": true, "von": true}

// TitleCase formats a name as "Primera Mayúscula" in each word and the rest
// in lowercase ("JUAN de la CRUZ" -> "Juan de la Cruz"). Emoji and symbols
// stay as they are.
func TitleCase(s string) string {
	words := strings.Fields(s)
	for i, w := range words {
		lw := strings.ToLower(w)
		if i > 0 && smallWords[lw] {
			words[i] = lw
			continue
		}
		parts := strings.Split(lw, "-")
		for j, p := range parts {
			r := []rune(p)
			for k, c := range r {
				if unicode.IsLetter(c) {
					r[k] = unicode.ToUpper(c)
					break
				}
				if unicode.IsDigit(c) {
					break
				}
			}
			parts[j] = string(r)
		}
		words[i] = strings.Join(parts, "-")
	}
	return strings.Join(words, " ")
}

// EditDetails gives the audit details of a contact edit: only the fields
// that really changed, as old and new value.
func EditDetails(oldName, oldCountry string, name, country *string) map[string]any {
	det := map[string]any{}
	if name != nil {
		if n := TitleCase(*name); n != oldName {
			det["old_name"], det["new_name"] = oldName, n
		}
	}
	if country != nil {
		if c := strings.ToUpper(strings.TrimSpace(*country)); c != oldCountry {
			det["old_country"], det["new_country"] = oldCountry, c
		}
	}
	return det
}
