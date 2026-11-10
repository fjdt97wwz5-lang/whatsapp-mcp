package store

import (
	"context"
	"database/sql"
	"fmt"
	"regexp"
	"strings"
	"time"
)

// Conversation is one chat with what the owner knows about it.
type Conversation struct {
	JID          string     `json:"jid"`
	Name         string     `json:"name"`
	IsGroup      bool       `json:"is_group"`
	Phone        string     `json:"phone,omitempty"` // digits, when the JID carries the number
	LastTime     *time.Time `json:"last_time,omitempty"`
	LastText     string     `json:"last_text,omitempty"`
	LastFromMe   bool       `json:"last_from_me"`
	LastSender   string     `json:"last_sender,omitempty"`
	Unanswered   bool       `json:"unanswered"` // last message is theirs
	Category     string     `json:"category,omitempty"`
	Country      string     `json:"country,omitempty"`           // set by the owner or Claude
	CountryGuess string     `json:"country_suggested,omitempty"` // from the phone prefix only
}

// ConvFilter selects conversations.
type ConvFilter struct {
	Query      string // name or number
	Kind       string // "", "contacts", "groups"
	Category   string // "" any, "-" none
	Country    string // "" any, "-" none
	Unanswered bool   // only chats whose last message is theirs
	MinWait    time.Duration
	MaxAge     time.Duration // only chats with activity in this window (0 = any)
	Limit      int
	Offset     int
}

// ListConversations returns chats, newest first.
func (s *Store) ListConversations(ctx context.Context, f ConvFilter) ([]Conversation, error) {
	if f.Limit <= 0 || f.Limit > 2000 {
		f.Limit = 50
	}
	var where []string
	var args []any
	arg := func(v any) string { args = append(args, v); return fmt.Sprintf("$%d", len(args)) }
	where = append(where, `c.jid NOT LIKE '%@broadcast'`, `c.jid NOT LIKE '%@newsletter'`)
	if q := strings.TrimSpace(f.Query); q != "" {
		like := "%" + escapeLike(q) + "%"
		cond := "c.name ILIKE " + arg(like) + " OR c.jid ILIKE " + arg(like)
		if d := DigitsOnly(q); len(d) >= 3 {
			cond += " OR c.jid LIKE " + arg("%"+d+"%")
		}
		where = append(where, "("+cond+")")
	}
	switch f.Kind {
	case "contacts":
		where = append(where, `c.jid NOT LIKE '%@g.us'`)
	case "groups":
		where = append(where, `c.jid LIKE '%@g.us'`)
	}
	switch f.Category {
	case "":
	case "-":
		where = append(where, `COALESCE(m.category,'') = ''`)
	default:
		where = append(where, "lower(m.category) = lower("+arg(f.Category)+")")
	}
	switch f.Country {
	case "":
	case "-":
		where = append(where, `COALESCE(m.country,'') = ''`)
	default:
		where = append(where, "m.country = "+arg(strings.ToUpper(f.Country)))
	}
	if f.Unanswered {
		where = append(where, `lm.is_from_me = false`)
		if f.MinWait > 0 {
			where = append(where, "lm.timestamp <= now() - make_interval(secs => "+arg(f.MinWait.Seconds())+")")
		}
	}
	if f.MaxAge > 0 {
		where = append(where, "c.last_message_time >= now() - make_interval(secs => "+arg(f.MaxAge.Seconds())+")")
	}
	q := `SELECT c.jid, COALESCE(c.name,''), c.last_message_time,
		COALESCE(lm.content,''), COALESCE(lm.media_type,''), COALESCE(lm.is_from_me,false), COALESCE(lm.sender,''), lm.timestamp,
		COALESCE(m.category,''), COALESCE(m.country,'')
		FROM wa_chats c
		LEFT JOIN LATERAL (
			SELECT content, media_type, is_from_me, sender, timestamp FROM wa_messages
			WHERE chat_jid = c.jid ORDER BY timestamp DESC LIMIT 1
		) lm ON true
		LEFT JOIN chat_meta m ON m.jid = c.jid
		WHERE ` + strings.Join(where, " AND ") + `
		ORDER BY COALESCE(lm.timestamp, c.last_message_time) DESC NULLS LAST
		LIMIT ` + arg(f.Limit) + ` OFFSET ` + arg(f.Offset)
	rows, err := s.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Conversation
	for rows.Next() {
		var c Conversation
		var chatTime, msgTime sql.NullTime
		var text, media string
		if err := rows.Scan(&c.JID, &c.Name, &chatTime, &text, &media, &c.LastFromMe, &c.LastSender, &msgTime, &c.Category, &c.Country); err != nil {
			return nil, err
		}
		c.IsGroup = strings.HasSuffix(c.JID, "@g.us")
		if strings.HasSuffix(c.JID, "@s.whatsapp.net") {
			c.Phone = strings.SplitN(c.JID, "@", 2)[0]
			c.CountryGuess = SuggestCountry(c.Phone)
		}
		switch {
		case msgTime.Valid:
			t := msgTime.Time
			c.LastTime = &t
			c.Unanswered = !c.LastFromMe
		case chatTime.Valid:
			t := chatTime.Time
			c.LastTime = &t
		}
		c.LastText = preview(text, media)
		out = append(out, c)
	}
	return out, rows.Err()
}

func preview(text, media string) string {
	text = strings.Join(strings.Fields(text), " ")
	if text == "" && media != "" {
		return "[" + media + "]"
	}
	if r := []rune(text); len(r) > 90 {
		return string(r[:90]) + "…"
	}
	return text
}

var countryRe = regexp.MustCompile(`^[A-Z]{2}$`)

// SetChatMeta stores the category and/or the country of a chat (nil = keep).
// It returns the previous values.
func (s *Store) SetChatMeta(ctx context.Context, jid string, category, country *string, by string) (oldCat, oldCountry string, err error) {
	if country != nil {
		c := strings.ToUpper(strings.TrimSpace(*country))
		if c != "" && !countryRe.MatchString(c) {
			return "", "", fmt.Errorf("country must be a 2-letter ISO code like CO, US or ES")
		}
		country = &c
	}
	if category != nil {
		c := strings.TrimSpace(*category)
		category = &c
	}
	tx, err := s.DB.BeginTx(ctx, nil)
	if err != nil {
		return "", "", err
	}
	defer tx.Rollback()
	var exists bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM wa_chats WHERE jid = $1)`, jid).Scan(&exists); err != nil {
		return "", "", err
	}
	if !exists {
		return "", "", fmt.Errorf("unknown chat %s", jid)
	}
	_ = tx.QueryRowContext(ctx, `SELECT category, country FROM chat_meta WHERE jid = $1 FOR UPDATE`, jid).Scan(&oldCat, &oldCountry)
	newCat, newCountry := oldCat, oldCountry
	if category != nil {
		newCat = *category
	}
	if country != nil {
		newCountry = *country
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO chat_meta (jid, category, country, updated_at, updated_by)
		VALUES ($1, $2, $3, now(), $4)
		ON CONFLICT (jid) DO UPDATE SET category = EXCLUDED.category, country = EXCLUDED.country,
			updated_at = now(), updated_by = EXCLUDED.updated_by`, jid, newCat, newCountry, by); err != nil {
		return "", "", err
	}
	return oldCat, oldCountry, tx.Commit()
}

// CountryCounts lists the countries in use, for filters.
func (s *Store) CountryCounts(ctx context.Context) (map[string]int, error) {
	rows, err := s.DB.QueryContext(ctx, `SELECT country, count(*) FROM chat_meta WHERE country <> '' GROUP BY country`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		out[c] = n
	}
	return out, rows.Err()
}

// dialCodes maps calling codes to ISO countries (most common first match).
var dialCodes = map[string]string{
	"1": "US", "52": "MX", "57": "CO", "34": "ES", "507": "PA", "58": "VE", "51": "PE", "593": "EC",
	"56": "CL", "54": "AR", "55": "BR", "506": "CR", "502": "GT", "503": "SV", "504": "HN", "505": "NI",
	"591": "BO", "595": "PY", "598": "UY", "53": "CU", "509": "HT", "33": "FR", "49": "DE", "39": "IT",
	"351": "PT", "44": "GB", "31": "NL", "32": "BE", "41": "CH", "43": "AT", "48": "PL", "353": "IE",
	"86": "CN", "852": "HK", "886": "TW", "81": "JP", "82": "KR", "91": "IN", "971": "AE", "966": "SA",
	"90": "TR", "7": "RU", "61": "AU", "64": "NZ", "27": "ZA", "234": "NG", "20": "EG", "972": "IL",
	"60": "MY", "65": "SG", "66": "TH", "84": "VN", "62": "ID", "63": "PH",
}

// SuggestCountry guesses a country from a phone number's prefix. It is only
// a suggestion: people often live somewhere else than their number says.
func SuggestCountry(digits string) string {
	digits = DigitsOnly(digits)
	for n := 3; n >= 1; n-- {
		if len(digits) > n {
			if c, ok := dialCodes[digits[:n]]; ok {
				return c
			}
		}
	}
	return ""
}
