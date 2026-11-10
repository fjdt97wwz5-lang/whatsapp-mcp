package panel

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net"
	"net/http"
	"sync"
	"time"
)

const (
	cookieName   = "wmcp_panel"
	sessionTTL   = 12 * time.Hour
	loginLinkTTL = 2 * time.Minute
)

type session struct {
	csrf    string
	expires time.Time
	remote  bool
}

type sessions struct {
	mu sync.Mutex
	m  map[string]*session
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}

func hashToken(t string) string {
	h := sha256.Sum256([]byte(t))
	return hex.EncodeToString(h[:])
}

func (s *sessions) create(remote bool) (id string, sess *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	for k, v := range s.m {
		if now.After(v.expires) {
			delete(s.m, k)
		}
	}
	id = randomHex(32)
	sess = &session{csrf: randomHex(16), expires: now.Add(sessionTTL), remote: remote}
	s.m[id] = sess
	return id, sess
}

func (s *sessions) get(r *http.Request) *session {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	sess := s.m[c.Value]
	if sess == nil || time.Now().After(sess.expires) {
		delete(s.m, c.Value)
		return nil
	}
	sess.expires = time.Now().Add(sessionTTL) // sliding
	return sess
}

func (s *sessions) drop(r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.mu.Lock()
		delete(s.m, c.Value)
		s.mu.Unlock()
	}
}

func setCookie(w http.ResponseWriter, id string, maxAge int) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: id, Path: "/panel", HttpOnly: true,
		SameSite: http.SameSiteStrictMode, MaxAge: maxAge})
}

// CreateLoginLink stores a one-time token that opens the panel without the
// access key. Only an administrator on the server can read the database
// settings, so only they can make one. It is valid for two minutes.
func CreateLoginLink(ctx context.Context, d *sql.DB) (string, error) {
	t := randomHex(24)
	_, err := d.ExecContext(ctx, `DELETE FROM panel_login WHERE expires_at < now() - interval '1 day'`)
	if err != nil {
		return "", err
	}
	_, err = d.ExecContext(ctx, `INSERT INTO panel_login (token_hash, expires_at) VALUES ($1, now() + make_interval(secs => $2))`,
		hashToken(t), loginLinkTTL.Seconds())
	return t, err
}

func useLoginLink(ctx context.Context, d *sql.DB, t string) bool {
	if len(t) < 20 {
		return false
	}
	res, err := d.ExecContext(ctx, `UPDATE panel_login SET used_at = now()
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > now()`, hashToken(t))
	if err != nil {
		return false
	}
	n, _ := res.RowsAffected()
	return n == 1
}

// limiter slows down guessing of the access key on the login form.
type limiter struct {
	mu    sync.Mutex
	fails map[string][]time.Time
}

func (l *limiter) blocked(ip string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	cut := time.Now().Add(-10 * time.Minute)
	keep := l.fails[ip][:0]
	for _, t := range l.fails[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	l.fails[ip] = keep
	return len(keep) >= 5
}

func (l *limiter) fail(ip string) {
	l.mu.Lock()
	l.fails[ip] = append(l.fails[ip], time.Now())
	l.mu.Unlock()
}

func clientIP(r *http.Request) net.IP {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}
