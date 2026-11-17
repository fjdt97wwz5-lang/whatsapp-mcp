package style

import (
	"context"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/policy"
)

// Logger is the small logging interface used here.
type Logger interface {
	Infof(string, ...any)
	Warnf(string, ...any)
}

// Run learns the style again every writing_style_relearn_days days (and the
// first time a few minutes after start, if it was never learned), while
// writing_style_enabled is on.
func (s *Store) Run(ctx context.Context, pol *policy.Policy, log Logger) {
	first := time.NewTimer(3 * time.Minute)
	defer first.Stop()
	tick := time.NewTicker(time.Hour)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-first.C:
		case <-tick.C:
		}
		s.maybeLearn(ctx, pol, log, time.Now())
	}
}

func (s *Store) maybeLearn(ctx context.Context, pol *policy.Policy, log Logger, now time.Time) bool {
	set, err := pol.Settings(ctx)
	if err != nil || !set.WritingStyleEnabled {
		return false
	}
	days := set.WritingStyleDays
	if days <= 0 {
		days = 7
	}
	last := s.LearnedAt(ctx)
	if !last.IsZero() && now.Sub(last) < time.Duration(days)*24*time.Hour {
		return false
	}
	p, err := s.Learn(ctx, policy.ActorSystem)
	if err != nil {
		pol.Audit(ctx, policy.ActorSystem, "style.learn", "", "error", map[string]any{"error": err.Error()})
		if log != nil {
			log.Warnf("writing style: %v", err)
		}
		return false
	}
	pol.Audit(ctx, policy.ActorSystem, "style.learn", "", "ok", map[string]any{"messages": p.MessagesUsed, "chats": p.Chats})
	if log != nil {
		log.Infof("writing style learned from %d messages", p.MessagesUsed)
	}
	return true
}
