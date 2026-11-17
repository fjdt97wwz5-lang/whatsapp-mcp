package ebay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Microsoft sign-in with the "device code" flow: the panel shows a short
// code, the owner types it at microsoft.com/devicelogin on any device and
// signs in there. No password ever passes through this program. Only the
// refresh token is kept, in a file only administrators can read.

// Scopes asked for: read mail (own and shared mailboxes) and the profile,
// plus offline_access for the refresh token. Nothing that can send mail.
const Scopes = "offline_access https://graph.microsoft.com/Mail.Read https://graph.microsoft.com/Mail.Read.Shared https://graph.microsoft.com/User.Read"

var (
	reGUID   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	reTenant = regexp.MustCompile(`^([0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}|[a-z0-9][a-z0-9.-]{1,200}\.[a-z]{2,}|organizations|common)$`)
	reMailbx = regexp.MustCompile(`^[a-z0-9._%+-]+@[a-z0-9.-]+\.[a-z]{2,}$`)
)

// ValidClient checks the Client ID and tenant typed in the panel.
func ValidClient(clientID, tenant string) error {
	if !reGUID.MatchString(clientID) {
		return errors.New("el ID de la aplicación (Client ID) tiene la forma 00000000-0000-0000-0000-000000000000")
	}
	if !reTenant.MatchString(strings.ToLower(tenant)) {
		return errors.New("el inquilino debe ser tu dominio (por ejemplo 4allfix.com), su ID, u organizations")
	}
	return nil
}

// Token is what is stored on disk.
type Token struct {
	ClientID string    `json:"client_id"`
	Tenant   string    `json:"tenant"`
	Refresh  string    `json:"refresh_token"`
	Access   string    `json:"-"`
	Expires  time.Time `json:"-"`
	Account  string    `json:"account"`
	Saved    time.Time `json:"saved"`
}

// DeviceCode is what the panel shows while signing in.
type DeviceCode struct {
	UserCode        string    `json:"user_code"`
	VerificationURI string    `json:"verification_uri"`
	Expires         time.Time `json:"expires"`
	deviceCode      string
	interval        time.Duration
}

// Graph talks to Microsoft sign-in and Microsoft Graph.
type Graph struct {
	TokenPath string // file holding the refresh token
	HTTP      *http.Client
	AuthBase  string // https://login.microsoftonline.com (tests override)
	APIBase   string // https://graph.microsoft.com/v1.0
	Lock      func(path string) error

	mu  sync.Mutex
	tok *Token
}

func (g *Graph) client() *http.Client {
	if g.HTTP != nil {
		return g.HTTP
	}
	return &http.Client{Timeout: 60 * time.Second}
}

func (g *Graph) authBase() string {
	if g.AuthBase != "" {
		return strings.TrimRight(g.AuthBase, "/")
	}
	return "https://login.microsoftonline.com"
}

func (g *Graph) apiBase() string {
	if g.APIBase != "" {
		return strings.TrimRight(g.APIBase, "/")
	}
	return "https://graph.microsoft.com/v1.0"
}

// Load reads the stored token, if any.
func (g *Graph) Load() (*Token, bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.tok != nil {
		t := *g.tok
		return &t, true
	}
	b, err := os.ReadFile(g.TokenPath)
	if err != nil {
		return nil, false
	}
	var t Token
	if json.Unmarshal(b, &t) != nil || t.Refresh == "" {
		return nil, false
	}
	g.tok = &t
	c := t
	return &c, true
}

func (g *Graph) save(t *Token) error {
	t.Saved = time.Now()
	b, _ := json.MarshalIndent(t, "", "  ")
	if err := os.MkdirAll(filepath.Dir(g.TokenPath), 0o700); err != nil {
		return err
	}
	tmp := g.TokenPath + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, g.TokenPath); err != nil {
		return err
	}
	if g.Lock != nil {
		_ = g.Lock(g.TokenPath)
	}
	g.tok = t
	return nil
}

// Forget deletes the stored token.
func (g *Graph) Forget() error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.tok = nil
	if err := os.Remove(g.TokenPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

type oauthErr struct {
	Error string `json:"error"`
	Desc  string `json:"error_description"`
}

func (g *Graph) postForm(ctx context.Context, u string, form url.Values, out any) (int, *oauthErr, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader(form.Encode()))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := g.client().Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		var oe oauthErr
		_ = json.Unmarshal(b, &oe)
		if oe.Error == "" {
			oe.Error = resp.Status
		}
		return resp.StatusCode, &oe, nil
	}
	return resp.StatusCode, nil, json.Unmarshal(b, out)
}

func shortDesc(oe *oauthErr) string {
	d := oe.Desc
	if i := strings.Index(d, "\r\n"); i > 0 {
		d = d[:i]
	}
	if d == "" {
		return oe.Error
	}
	return oe.Error + ": " + d
}

// StartDevice asks Microsoft for a sign-in code.
func (g *Graph) StartDevice(ctx context.Context, clientID, tenant string) (*DeviceCode, error) {
	if err := ValidClient(clientID, tenant); err != nil {
		return nil, err
	}
	var r struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	}
	_, oe, err := g.postForm(ctx, g.authBase()+"/"+url.PathEscape(strings.ToLower(tenant))+"/oauth2/v2.0/devicecode",
		url.Values{"client_id": {clientID}, "scope": {Scopes}}, &r)
	if err != nil {
		return nil, err
	}
	if oe != nil {
		return nil, fmt.Errorf("Microsoft no dio el código: %s", shortDesc(oe))
	}
	if r.Interval <= 0 {
		r.Interval = 5
	}
	return &DeviceCode{UserCode: r.UserCode, VerificationURI: r.VerificationURI, Expires: time.Now().Add(time.Duration(r.ExpiresIn) * time.Second),
		deviceCode: r.DeviceCode, interval: time.Duration(r.Interval) * time.Second}, nil
}

type tokenResp struct {
	Access    string `json:"access_token"`
	Refresh   string `json:"refresh_token"`
	ExpiresIn int    `json:"expires_in"`
}

// WaitDevice waits until the owner finishes signing in, then stores the
// token.
func (g *Graph) WaitDevice(ctx context.Context, clientID, tenant string, dc *DeviceCode) (*Token, error) {
	wait := dc.interval
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(wait):
		}
		if time.Now().After(dc.Expires) {
			return nil, errors.New("se acabó el tiempo para escribir el código; pulsa Conectar otra vez")
		}
		var r tokenResp
		_, oe, err := g.postForm(ctx, g.authBase()+"/"+url.PathEscape(strings.ToLower(tenant))+"/oauth2/v2.0/token", url.Values{
			"grant_type": {"urn:ietf:params:oauth:grant-type:device_code"}, "client_id": {clientID}, "device_code": {dc.deviceCode}}, &r)
		if err != nil {
			continue // network hiccup: try again
		}
		if oe != nil {
			switch oe.Error {
			case "authorization_pending":
				continue
			case "slow_down":
				wait += 5 * time.Second
				continue
			case "authorization_declined":
				return nil, errors.New("rechazaste el permiso en la página de Microsoft")
			case "expired_token", "bad_verification_code":
				return nil, errors.New("el código venció; pulsa Conectar otra vez")
			}
			return nil, fmt.Errorf("Microsoft no aceptó la conexión: %s", shortDesc(oe))
		}
		if r.Refresh == "" {
			return nil, errors.New("Microsoft no dio permiso para seguir conectado (falta offline_access)")
		}
		t := &Token{ClientID: clientID, Tenant: strings.ToLower(tenant), Refresh: r.Refresh, Access: r.Access,
			Expires: time.Now().Add(time.Duration(r.ExpiresIn-60) * time.Second)}
		g.mu.Lock()
		err = g.save(t)
		g.mu.Unlock()
		if err != nil {
			return nil, err
		}
		if acct, err := g.Me(ctx); err == nil {
			g.mu.Lock()
			t.Account = acct
			_ = g.save(t)
			g.mu.Unlock()
		}
		return t, nil
	}
}

// ErrNotConnected means nobody signed in yet (or the sign-in was removed).
var ErrNotConnected = errors.New("no hay una cuenta de Microsoft conectada")

// ErrSignInAgain means Microsoft no longer accepts the stored sign-in.
var ErrSignInAgain = errors.New("Microsoft pide entrar otra vez: pulsa Conectar en Avisos eBay")

func (g *Graph) accessToken(ctx context.Context) (string, error) {
	if _, ok := g.Load(); !ok {
		return "", ErrNotConnected
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	t := g.tok
	if t.Access != "" && time.Now().Before(t.Expires) {
		return t.Access, nil
	}
	var r tokenResp
	_, oe, err := g.postForm(ctx, g.authBase()+"/"+url.PathEscape(t.Tenant)+"/oauth2/v2.0/token", url.Values{
		"grant_type": {"refresh_token"}, "client_id": {t.ClientID}, "refresh_token": {t.Refresh}, "scope": {Scopes}}, &r)
	if err != nil {
		return "", err
	}
	if oe != nil {
		if oe.Error == "invalid_grant" || oe.Error == "interaction_required" {
			return "", ErrSignInAgain
		}
		return "", fmt.Errorf("Microsoft: %s", shortDesc(oe))
	}
	nt := *t
	nt.Access, nt.Expires = r.Access, time.Now().Add(time.Duration(r.ExpiresIn-60)*time.Second)
	if r.Refresh != "" && r.Refresh != t.Refresh {
		nt.Refresh = r.Refresh
		if err := g.save(&nt); err != nil {
			return "", err
		}
	} else {
		g.tok = &nt
	}
	return nt.Access, nil
}

func (g *Graph) get(ctx context.Context, u string, out any) error {
	at, err := g.accessToken(ctx)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+at)
	req.Header.Set("Prefer", `outlook.body-content-type="text"`)
	resp, err := g.client().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if resp.StatusCode != http.StatusOK {
		var e struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if resp.StatusCode == http.StatusUnauthorized {
			g.mu.Lock()
			if g.tok != nil {
				g.tok.Access = ""
			}
			g.mu.Unlock()
		}
		if e.Error.Code != "" {
			return fmt.Errorf("Microsoft Graph %s: %s", e.Error.Code, e.Error.Message)
		}
		return fmt.Errorf("Microsoft Graph respondió %s", resp.Status)
	}
	return json.Unmarshal(b, out)
}

// Me returns the signed-in account's address.
func (g *Graph) Me(ctx context.Context) (string, error) {
	var r struct {
		Mail string `json:"mail"`
		UPN  string `json:"userPrincipalName"`
	}
	if err := g.get(ctx, g.apiBase()+"/me?$select=mail,userPrincipalName", &r); err != nil {
		return "", err
	}
	if r.Mail != "" {
		return strings.ToLower(r.Mail), nil
	}
	return strings.ToLower(r.UPN), nil
}

// Mail is one message from the mailbox.
type Mail struct {
	ID               string    `json:"id"`
	InternetID       string    `json:"internetMessageId"`
	Subject          string    `json:"subject"`
	ReceivedDateTime time.Time `json:"receivedDateTime"`
	From             struct {
		EmailAddress struct {
			Address string `json:"address"`
		} `json:"emailAddress"`
	} `json:"from"`
	Body struct {
		ContentType string `json:"contentType"`
		Content     string `json:"content"`
	} `json:"body"`
}

// SaleMails lists mails from eBay received since "since", oldest first.
// mailbox empty = the signed-in account's own mailbox.
func (g *Graph) SaleMails(ctx context.Context, mailbox string, since time.Time) ([]Mail, error) {
	base := g.apiBase() + "/me/messages"
	if mailbox != "" {
		mailbox = strings.ToLower(mailbox)
		if !reMailbx.MatchString(mailbox) {
			return nil, errors.New("el buzón no es una dirección válida")
		}
		base = g.apiBase() + "/users/" + url.PathEscape(mailbox) + "/messages"
	}
	q := url.Values{}
	q.Set("$filter", fmt.Sprintf("receivedDateTime ge %s and from/emailAddress/address eq '%s'", since.UTC().Format(time.RFC3339), SaleSender))
	q.Set("$orderby", "receivedDateTime asc")
	q.Set("$select", "id,internetMessageId,subject,receivedDateTime,from,body")
	q.Set("$top", "25")
	u := base + "?" + q.Encode()
	var out []Mail
	for page := 0; u != "" && page < 8; page++ {
		var r struct {
			Value []Mail `json:"value"`
			Next  string `json:"@odata.nextLink"`
		}
		if err := g.get(ctx, u, &r); err != nil {
			return nil, err
		}
		out = append(out, r.Value...)
		u = ""
		if strings.HasPrefix(r.Next, g.apiBase()+"/") {
			u = r.Next
		}
	}
	return out, nil
}
