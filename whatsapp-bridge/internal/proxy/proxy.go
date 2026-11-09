// Package proxy lets an MCP client that only speaks stdio (Claude Desktop
// on the Mac) use the connector running on the server over Tailscale. It
// forwards every JSON-RPC message between stdin/stdout and the server's
// /mcp endpoint, adding the access token.
package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type tokenTransport struct {
	token string
	base  http.RoundTripper
}

func (t *tokenTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	r = r.Clone(r.Context())
	r.Header.Set("Authorization", "Bearer "+t.token)
	return t.base.RoundTrip(r)
}

// Run forwards until stdin closes or ctx ends.
func Run(ctx context.Context, endpoint, token string, stdin io.ReadCloser, stdout io.WriteCloser) error {
	if endpoint == "" || token == "" {
		return errors.New("server URL and token are required")
	}
	client := &http.Client{Transport: &tokenTransport{token: token, base: http.DefaultTransport}, Timeout: 0}
	remoteT := &mcp.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client}
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	remote, err := remoteT.Connect(cctx)
	cancel()
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", endpoint, err)
	}
	defer remote.Close()
	local, err := (&mcp.IOTransport{Reader: stdin, Writer: stdout}).Connect(ctx)
	if err != nil {
		return err
	}
	defer local.Close()

	errc := make(chan error, 2)
	pipe := func(from, to mcp.Connection) {
		for {
			msg, err := from.Read(ctx)
			if err != nil {
				errc <- err
				return
			}
			if err := to.Write(ctx, msg); err != nil {
				errc <- err
				return
			}
		}
	}
	go pipe(local, remote)
	go pipe(remote, local)
	err = <-errc
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) {
		return nil
	}
	return err
}
