package wa

import "time"

// Number returns the linked number as +digits ("" when not linked).
func (s Status) Number() string {
	if s.Account == "" {
		return ""
	}
	return "+" + s.Account
}

// Summary is one plain line in Spanish for the panel, "Estado" and
// /api/health. It avoids symbols that old Windows console fonts lack.
func (s Status) Summary() string {
	if !s.Paired {
		return "WhatsApp NO vinculado. Usa \"Vincular WhatsApp\" (QR) o el código de 8 letras."
	}
	who := s.Number()
	if s.Name != "" {
		who += " (" + s.Name + ")"
	}
	if s.Connected && s.LoggedIn {
		line := "WhatsApp VINCULADO y conectado: " + who
		if s.LastConnected != nil {
			line += ", conectado desde " + fmtTime(*s.LastConnected)
		}
		return line
	}
	line := "WhatsApp vinculado a " + who + " pero SIN conexión ahora"
	if s.LastConnected != nil {
		line += "; última conexión " + fmtTime(*s.LastConnected)
	}
	if s.LastEvent != "" {
		line += " (" + s.LastEvent + ")"
	}
	return line
}

func fmtTime(t time.Time) string {
	t = t.Local()
	now := time.Now()
	if y, m, d := now.Date(); t.Year() == y && t.Month() == m && t.Day() == d {
		return "hoy " + t.Format("15:04")
	}
	return t.Format("02/01/2006 15:04")
}
