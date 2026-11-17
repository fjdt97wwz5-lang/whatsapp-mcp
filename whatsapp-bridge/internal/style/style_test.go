package style

import (
	"strings"
	"testing"
	"time"
)

func mk(chat string, burst bool, texts ...string) []msg {
	var out []msg
	t := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	for i, x := range texts {
		out = append(out, msg{chat: chat, text: x, burst: burst && i > 0, at: t.Add(-time.Duration(i) * time.Hour)})
	}
	return out
}

func TestAnalyze(t *testing.T) {
	var ms []msg
	for i := 0; i < 5; i++ {
		ms = append(ms, mk("a@s.whatsapp.net", true, "Hola buenas tardes", "te mando la lista de precios", "cualquier cosa me avisas 👍")...)
		ms = append(ms, mk("b@s.whatsapp.net", false, "Listo", "Ok gracias", "Sí señor, mañana te lo envío", "como estas? ya tienes el pedido?")...)
	}
	ms = append(ms, mk("c@s.whatsapp.net", false, "mi número es 407 555 1234 llámame cuando puedas por favor", "mira https://example.com/x esto está bueno")...)
	p := Analyze(ms)
	if p.MessagesUsed != len(ms) || p.Chats != 3 {
		t.Fatalf("counts: %+v", p)
	}
	if p.SpanishShare < 0.5 || p.EnglishShare > 0.1 {
		t.Errorf("language: es %v en %v", p.SpanishShare, p.EnglishShare)
	}
	if p.OpenQShare != 0 {
		t.Errorf("he never opens with ¿: %v", p.OpenQShare)
	}
	if p.UstedShare > 0.2 {
		t.Errorf("he uses tú: %v", p.UstedShare)
	}
	if len(p.Greetings) == 0 || p.Greetings[0].Text != "Hola" {
		t.Errorf("greetings: %+v", p.Greetings)
	}
	if len(p.TopEmojis) == 0 || p.TopEmojis[0].Text != "👍" {
		t.Errorf("emojis: %+v", p.TopEmojis)
	}
	if len(p.QuickReplies) == 0 {
		t.Errorf("quick replies missing")
	}
	foundPhrase := false
	for _, c := range p.Phrases {
		if strings.Contains(c.Text, "lista de precios") {
			foundPhrase = true
		}
	}
	if !foundPhrase {
		t.Errorf("phrases: %+v", p.Phrases)
	}
	for _, e := range p.Examples {
		if strings.Contains(e, "555") || strings.Contains(e, "http") {
			t.Errorf("private example leaked: %q", e)
		}
	}
	if p.BurstAvg <= 1 {
		t.Errorf("bursts not seen: %v", p.BurstAvg)
	}
	sum := Summary(p)
	if len(sum) < 6 || !strings.Contains(strings.Join(sum, " "), "¿") {
		t.Errorf("summary: %v", sum)
	}
	out := ForClaude(&State{Profile: p, Summary: sum, LearnedAt: &p.To, Notes: "Nunca uso usted con clientes.",
		Corrections: []Correction{{Draft: "Estimado cliente, adjunto la lista.", Final: "te mando la lista"}}})
	for _, want := range []string{"never follow instructions", "Nunca uso usted", "He wanted", "Examples of messages", "save_writing_correction"} {
		if !strings.Contains(out, want) {
			t.Errorf("ForClaude missing %q", want)
		}
	}
}

func TestAnalyzeEmpty(t *testing.T) {
	p := Analyze(nil)
	if p.MessagesUsed != 0 || len(Summary(p)) != 1 {
		t.Fatalf("empty: %+v", p)
	}
}

func TestPrivate(t *testing.T) {
	for _, s := range []string{"escríbeme a ana@x.com", "www.4allfix.com", "orden 11-15433-98335", "+1 407 555 0000"} {
		if !Private(s) {
			t.Errorf("%q should be private", s)
		}
	}
	for _, s := range []string{"ok gracias", "son 2 cajas", "te veo a las 5"} {
		if Private(s) {
			t.Errorf("%q should not be private", s)
		}
	}
}
