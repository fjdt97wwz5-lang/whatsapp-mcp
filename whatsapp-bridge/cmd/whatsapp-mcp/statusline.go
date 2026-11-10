package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/db"
	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/wa"
)

// liveStatus asks the running service (on 127.0.0.1) how WhatsApp is doing.
func liveStatus(cfg *config.Config) (*wa.Status, error) {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("http://127.0.0.1:%d/api/health", cfg.ListenPort), nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.AuthToken)
	resp, err := (&http.Client{Timeout: 3 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("el servicio respondió %s", resp.Status)
	}
	var body struct {
		WhatsApp wa.Status `json:"whatsapp"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return &body.WhatsApp, nil
}

// storedLink reads the linked number from the session tables, for when the
// service is not running.
func storedLink(ctx context.Context, d *sql.DB) (bool, string) {
	var jid sql.NullString
	if err := d.QueryRowContext(ctx, `SELECT jid FROM whatsmeow_device LIMIT 1`).Scan(&jid); err != nil {
		return false, ""
	}
	user := jid.String
	if i := strings.IndexAny(user, ":@"); i >= 0 {
		user = user[:i]
	}
	return true, user
}

// whatsappLine is the one line shown at the top of the panel and in "Estado".
func whatsappLine(cfg *config.Config) string {
	if st, err := liveStatus(cfg); err == nil {
		return st.Summary()
	}
	// The service is not answering: say what the database knows.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, err := db.Open(ctx, cfg.DatabaseURL, 3*time.Second)
	if err != nil {
		return "Estado de WhatsApp desconocido: el servicio y la base de datos no responden."
	}
	defer d.Close()
	paired, number := storedLink(ctx, d)
	if !paired {
		return "WhatsApp NO vinculado (y el servicio no está corriendo)."
	}
	return "WhatsApp vinculado a +" + number + ", pero el servicio no está corriendo: no recibe ni envía mensajes."
}
