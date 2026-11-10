package httpapi

import (
	"encoding/json"
	"testing"

	"github.com/fjdt97wwz5-lang/whatsapp-mcp/whatsapp-bridge/internal/config"
)

func TestOtherUse(t *testing.T) {
	c := &Cloud{cfg: &config.Config{CloudPort: 8767, CloudPublicPort: 10000}}
	var st serveStatus
	_ = json.Unmarshal([]byte(`{"AllowFunnel":{"srv.tail.ts.net:443":true,"srv.tail.ts.net:10000":true},
	 "Web":{"srv.tail.ts.net:443":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:3000"}}},
	        "srv.tail.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:8767"}}}}}`), &st)
	if o := c.otherUse(st); o != "" {
		t.Fatalf("our own handler reported as other use: %s", o)
	}
	_ = json.Unmarshal([]byte(`{"Web":{"srv.tail.ts.net:10000":{"Handlers":{"/":{"Proxy":"http://127.0.0.1:9000"}}}}}`), &st)
	if o := c.otherUse(st); o == "" {
		t.Fatal("another app on 10000 not detected")
	}
}
