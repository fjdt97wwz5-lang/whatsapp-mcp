# WhatsApp MCP (hardened fork)

A WhatsApp connector for Claude, based on [lharries/whatsapp-mcp](https://github.com/lharries/whatsapp-mcp),
rewritten as one Go program meant to run as a Windows service on a home server
and be reached from other machines over Tailscale.

Spanish user guide: [docs/GUIA.md](docs/GUIA.md).

## What changed from upstream

| Upstream | This fork |
| --- | --- |
| Go bridge + separate Python MCP server | One binary: bridge, MCP (Streamable HTTP), CLI, Windows service |
| REST API on `:8080`, all interfaces, no auth | Listens on `127.0.0.1` and Tailscale addresses only (re-detected every 20 s), every request needs a token; wildcard binds are refused |
| SQLite for messages and session | PostgreSQL (private instance created by the installer, `127.0.0.1:5544`, SCRAM) |
| Sends to anyone | Sends only to an approved list of contacts/groups; rate limit; master switch |
| Attachments from any path | Any path by default, optional "only from this folder" (symlinks resolved) and size limit |
| Downloaded filenames used as-is | Sanitized (no paths, reserved Windows names, control/bidi characters) and kept inside the media folder |
| No record of actions | Audit log of sends (including refused ones), settings and list changes, downloads, pairing |
| Message text passed to the model as-is | Read tools mark message content as untrusted; send tools are annotated as destructive |

Settings live in the database and can be changed by hand (web panel, CLI/menu) or
by Claude through MCP tools. Settings marked hand-only (`send_only_to_approved`,
`mcp_can_change_settings`, `cloud_access_enabled`, `panel_remote`) cannot be
changed through MCP.

## 2.0.3

- The panel shortcut and the installer's "Abrir el panel ahora" open the
  browser again. An elevated process that hands a web address to
  explorer.exe only gets File Explorer, so the address now goes into a
  private `.url` shortcut that explorer.exe opens as the signed-in user.
- If the panel page does not use the link within 12 seconds, a small window
  shows the address with a "Copiar" button (and the console prints it); it
  closes by itself once the panel opens.
- The one-time token travels after `#` and is spent only by the panel page's
  own script (`POST /panel/api/login-link`, same origin only), so anything
  that merely fetches the address cannot use it up. It now lasts 10 minutes.

## 2.0.2

- Installer fix for upgrades: before copying files it stops the services and
  waits (up to 60 s) until Windows reports them stopped, closes any
  `whatsapp-mcp.exe` still running (an open Panel or Vincular window) and the
  bundled Postgres processes, retries the exe copy (moving a locked one aside
  as a last resort), and starts both services again at the end
  (`installer/stop-services.ps1`). No app changes since 2.0.1.

## 2.0.1

- **Web panel** at `http://127.0.0.1:8765/panel/` (Spanish): status banner, QR /
  8-character pairing inside the page, conversations with search and filters and
  an "Aprobar" button per chat, approved list (flag selector, phone masks, Title
  Case names), settings as switches, audit log, connection info, history import.
  Opened from the Start menu with a one-time link (2 min, single use, loopback
  only, created by an elevated `whatsapp-mcp panel`); otherwise login with the
  access key (rate limited). Session cookie is HttpOnly + SameSite=Strict,
  changes need a CSRF token, strict CSP. Other Tailscale devices only when
  `panel_remote` is on.
- **Follow-up data**: per-chat category (editable list in `chat_categories`) and
  country (the phone prefix is only shown as a suggestion), "unanswered" filter.
  New MCP tools: `list_conversations`, `list_unanswered_chats`,
  `get_chat_categories`, `set_chat_category`, `set_chat_country`,
  `get_chat_transcript`.
- **Cloud access via Tailscale Funnel**: a separate listener on
  `127.0.0.1:8767` that serves only MCP, only while `cloud_access_enabled` is on,
  only with a separate cloud key (`/mcp/k/<key>` for claude.ai custom connectors,
  or Bearer). Rate limited, wrong keys throttled and logged, calls audited as
  `cloud`. The panel can publish/withdraw it (`tailscale funnel --bg
  --https=10000 http://127.0.0.1:8767`) and refuses to touch a Funnel port that
  another app already uses. The panel and the REST API are never reachable there.
- QR fix: the console QR was taller than a default console window; pairing now
  also writes a self-refreshing HTML page and the panel shows it at full size.
- Health endpoint and `status` report linked / connected / number / last
  connection. After a logout from the phone the service prepares a new device so
  it can be linked again without a restart.

## Layout

```
whatsapp-bridge/
  cmd/whatsapp-mcp/      CLI, install/uninstall, menu, Windows service entry point
  internal/config        config.json (secrets generated locally, file ACL'd)
  internal/db            PostgreSQL connection and schema migrations
  internal/store         chats and messages
  internal/policy        settings, approved list, rate limit, attachment rules, audit log
  internal/wa            whatsmeow client, history sync, media, sending, filename sanitizing
  internal/mcptools      MCP tools (25)
  internal/httpapi       HTTP server: network + token guard, /mcp, /api/*, /panel; cloud (Funnel) listener
  internal/panel         web panel (embedded static files, login links, JSON API)
  internal/pgsetup       creates and registers the private PostgreSQL
  internal/sqlitemigrate imports the old messages.db
  internal/proxy         stdio <-> HTTP bridge for Claude Desktop on another machine
installer/               NSIS script and build.sh
docs/GUIA.md             user guide (Spanish)
```

## Build

```
installer/build.sh 2.0.3     # needs Go, makensis, python3, curl
```

Produces `dist/WhatsAppMCP-Setup-2.0.3.exe` plus macOS binaries for the
`connect` helper. The installer bundles PostgreSQL 17 for Windows
(zonky embedded-postgres-binaries, checksum-pinned) and the MSVC runtime DLLs.

## Commands

`whatsapp-mcp help` lists them: `install`, `uninstall [--purge]`, `pair [--phone]`,
`unlink`, `status`, `migrate-sqlite --from messages.db`, `settings [set K V]`,
`allow list|add|remove`, `audit`, `connection-info`, `rotate-token`,
`panel [--open section]`, `menu`,
`connect --url` (client side), `version`.

## Tests

```
cd whatsapp-bridge
go test ./...                                    # unit tests
WHATSAPP_MCP_TEST_DB=postgres://... go test ./internal/policy   # with a real PostgreSQL
```

## License

MIT, as upstream. whatsmeow is MPL-2.0. See `installer/THIRD-PARTY.txt`.
