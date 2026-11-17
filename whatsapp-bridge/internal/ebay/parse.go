// Package ebay reads the eBay "You made the sale" e-mails from the owner's
// Microsoft 365 mailbox and sends a WhatsApp notice, then a reminder, to the
// person who takes the packages to USPS.
package ebay

import (
	"errors"
	"fmt"
	"html"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// SaleSender is the only address whose mails count as sales.
const SaleSender = "ebay@ebay.com"

// SubjectPrefix starts the subject of a sale mail.
const SubjectPrefix = "You made the sale for"

// Sale is what goes into the WhatsApp message. The buyer's name and street
// are never kept: only city and state.
type Sale struct {
	Item     string
	Quantity int
	Place    string // "Chicago, IL"
	ShipBy   time.Time
	ShipText string // as written in the mail when it can't be read as a date
	Order    string
}

var (
	reTags     = regexp.MustCompile(`(?s)<[^>]*>`)
	reBreaks   = regexp.MustCompile(`(?i)<br\s*/?>|</(p|div|h[1-6]|td|tr|li|table)>`)
	reHidden   = regexp.MustCompile(`(?is)<(style|script|head)[^>]*>.*?</(style|script|head)>`)
	reSpaces   = regexp.MustCompile(`[ \t\x{00A0}]+`)
	reCityLine = regexp.MustCompile(`^(.+?),\s*([A-Z]{2})\s+[A-Z0-9][A-Z0-9 -]{2,9}$`)
	reOrder    = regexp.MustCompile(`\b\d{2}-\d{5}-\d{5}\b`)
	reURLish   = regexp.MustCompile(`(?i)(https?://\S+|www\.\S+|\S+@\S+)`)
	reInt      = regexp.MustCompile(`\d+`)
)

// HTMLToText turns a mail body into lines of plain text.
func HTMLToText(s string) string {
	s = reHidden.ReplaceAllString(s, "")
	s = reBreaks.ReplaceAllString(s, "\n")
	s = reTags.ReplaceAllString(s, "")
	return html.UnescapeString(s)
}

// cleanLine drops invisible characters eBay pads its mails with.
func cleanLine(l string) string {
	l = strings.Map(func(r rune) rune {
		switch {
		case r == '\u034f' || r == '\u200b' || r == '\u200c' || r == '\u200d' || r == '\ufeff' || r == '\u2060':
			return -1
		case unicode.Is(unicode.Mn, r):
			return -1
		}
		return r
	}, l)
	return strings.TrimSpace(reSpaces.ReplaceAllString(l, " "))
}

func lines(body string) []string {
	var out []string
	for _, l := range strings.Split(strings.ReplaceAll(body, "\r", ""), "\n") {
		if l = cleanLine(l); l != "" {
			out = append(out, l)
		}
	}
	return out
}

func norm(s string) string {
	s = strings.ToLower(strings.ReplaceAll(s, "’", "'"))
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ":"))
}

// valueAfter finds "Label: value" or "Label:" followed by the value on the
// next line.
func valueAfter(ls []string, label string) (string, int) {
	label = norm(label)
	for i, l := range ls {
		n := norm(l)
		if n == label {
			if i+1 < len(ls) {
				return ls[i+1], i + 1
			}
			return "", -1
		}
		if strings.HasPrefix(n, label+":") {
			return strings.TrimSpace(l[strings.Index(l, ":")+1:]), i
		}
	}
	return "", -1
}

// titleCase turns "CHICAGO" or "new york" into "Chicago", "New York".
func titleCase(s string) string {
	ws := strings.Fields(strings.ToLower(s))
	for i, w := range ws {
		r := []rune(w)
		r[0] = unicode.ToUpper(r[0])
		ws[i] = string(r)
	}
	return strings.Join(ws, " ")
}

func safeText(s string, n int) string {
	s = strings.TrimSpace(reURLish.ReplaceAllString(s, ""))
	r := []rune(s)
	if len(r) > n {
		s = strings.TrimSpace(string(r[:n])) + "…"
	}
	return s
}

// ParseMail reads a sale mail. body may be HTML or plain text.
func ParseMail(subject, body string) (Sale, error) {
	var s Sale
	subject = cleanLine(subject)
	if !strings.HasPrefix(strings.ToLower(subject), strings.ToLower(SubjectPrefix)) {
		return s, errors.New("not a sale mail")
	}
	item := strings.TrimSpace(subject[len(SubjectPrefix):])
	item = strings.TrimSuffix(item, "...")
	if strings.HasSuffix(subject, "...") {
		item = strings.TrimSpace(item) + "…"
	}
	s.Item = safeText(item, 100)
	if strings.Contains(body, "<") && strings.Contains(body, ">") {
		body = HTMLToText(body)
	}
	ls := lines(body)

	s.Quantity = 1
	if v, _ := valueAfter(ls, "Quantity sold"); v != "" {
		if m := reInt.FindString(v); m != "" {
			if n, err := strconv.Atoi(m); err == nil && n > 0 && n < 10000 {
				s.Quantity = n
			}
		}
	} else if v, _ := valueAfter(ls, "Quantity"); v != "" {
		if m := reInt.FindString(v); m != "" {
			if n, err := strconv.Atoi(m); err == nil && n > 0 && n < 10000 {
				s.Quantity = n
			}
		}
	}

	if v, _ := valueAfter(ls, "Order"); v != "" {
		s.Order = reOrder.FindString(v)
	}
	if s.Order == "" {
		s.Order = reOrder.FindString(strings.Join(ls, " "))
	}

	if v, _ := valueAfter(ls, "Ship by"); v != "" {
		s.ShipText = safeText(v, 40)
		for _, f := range []string{"Jan 2, 2006", "January 2, 2006", "Jan 2 2006", "Mon, Jan 2", "Jan 2"} {
			if t, err := time.Parse(f, v); err == nil {
				s.ShipBy = t
				break
			}
		}
	}

	_, at := valueAfter(ls, "Your buyer's shipping details")
	if at < 0 {
		_, at = valueAfter(ls, "Ship to")
	}
	if at >= 0 {
		// The address is a few lines: name, street, "CITY, ST ZIP", country.
		var block []string
		for j := at; j < len(ls) && j < at+7; j++ {
			n := norm(ls[j])
			if j > at && (strings.HasPrefix(n, "ship by") || strings.HasPrefix(n, "hi ") || strings.HasPrefix(n, "order")) {
				break
			}
			block = append(block, ls[j])
		}
		for _, l := range block {
			if m := reCityLine.FindStringSubmatch(l); m != nil {
				city := m[1]
				if k := strings.LastIndex(city, ","); k >= 0 { // "street, CITY"
					city = city[k+1:]
				}
				s.Place = titleCase(strings.TrimSpace(city)) + ", " + m[2]
				break
			}
		}
		if s.Place == "" && len(block) >= 2 {
			// Outside the US: the line before the country, plus the country.
			last := block[len(block)-1]
			prev := block[len(block)-2]
			s.Place = titleCase(prev) + ", " + titleCase(last)
		}
		s.Place = safeText(s.Place, 60)
	}
	if s.Item == "" {
		return s, errors.New("the mail has no item")
	}
	return s, nil
}

var meses = []string{"enero", "febrero", "marzo", "abril", "mayo", "junio", "julio", "agosto", "septiembre", "octubre", "noviembre", "diciembre"}

// SpanishDate writes "16 de noviembre".
func SpanishDate(t time.Time) string {
	return fmt.Sprintf("%d de %s", t.Day(), meses[t.Month()-1])
}

// SpanishTime writes "5:00 p.m.".
func SpanishTime(hhmm string) string {
	t, err := time.Parse("15:04", hhmm)
	if err != nil {
		return hhmm
	}
	ampm := "a.m."
	if t.Hour() >= 12 {
		ampm = "p.m."
	}
	h := t.Hour() % 12
	if h == 0 {
		h = 12
	}
	return fmt.Sprintf("%d:%02d %s", h, t.Minute(), ampm)
}

// Line is how one sale reads in the WhatsApp message.
func (s Sale) Line() string {
	var b strings.Builder
	b.WriteString(s.Item)
	if s.Quantity == 1 {
		b.WriteString(", 1 unidad")
	} else {
		fmt.Fprintf(&b, ", %d unidades", s.Quantity)
	}
	if s.Place != "" {
		b.WriteString(", para " + s.Place)
	}
	switch {
	case !s.ShipBy.IsZero():
		b.WriteString(", enviar a más tardar el " + SpanishDate(s.ShipBy))
	case s.ShipText != "" && s.ShipText[0] >= '0' && s.ShipText[0] <= '9':
		b.WriteString(", enviar a más tardar el " + s.ShipText) // "16 de noviembre"
	case s.ShipText != "":
		b.WriteString(", enviar a más tardar: " + s.ShipText)
	}
	return b.String()
}

// Compose fills a template with the sales.
func Compose(template string, sales []Sale, cutoff string) string {
	var ls []string
	for _, s := range sales {
		l := s.Line()
		if len(sales) > 1 {
			l = "- " + l
		}
		ls = append(ls, l)
	}
	out := strings.ReplaceAll(template, "{ventas}", strings.Join(ls, "\n"))
	out = strings.ReplaceAll(out, "{hora}", SpanishTime(cutoff))
	return strings.NewReplacer("a.m..", "a.m.", "p.m..", "p.m.").Replace(out)
}

// ReminderAt is when the reminder for a sale notified at "notified" goes
// out: minutes later, except that a reminder that would fall after the
// cutoff (when Ana leaves for USPS) goes at the cutoff: that same day if the
// notice went out before the cutoff, otherwise the next day. One that would
// fall at night (before 7:00) waits for that day's cutoff. Reminders due at
// the same moment go out in one message.
func ReminderAt(notified time.Time, minutes int, cutoff string, loc *time.Location) time.Time {
	if loc == nil {
		loc = time.UTC
	}
	ct, err := time.Parse("15:04", cutoff)
	if err != nil {
		ct, _ = time.Parse("15:04", "17:00")
	}
	at := func(day time.Time, h, m int) time.Time {
		return time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, loc)
	}
	n := notified.In(loc)
	d := n.Add(time.Duration(minutes) * time.Minute)
	switch {
	case d.Before(at(d, 7, 0)):
		return at(d, ct.Hour(), ct.Minute())
	case d.After(at(d, ct.Hour(), ct.Minute())):
		if c := at(n, ct.Hour(), ct.Minute()); n.Before(c) {
			return c
		}
		return at(d.AddDate(0, 0, 1), ct.Hour(), ct.Minute())
	}
	return d
}
