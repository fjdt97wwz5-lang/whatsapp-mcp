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

Settings live in the database and can be changed by hand (CLI/menu) or by Claude
through MCP tools. `send_only_to_approved` and `mcp_can_change_settings` can only
be changed by hand.

## Layout

```
whatsapp-bridge/
  cmd/whatsapp-mcp/      CLI, install/uninstall, menu, Windows service entry point
  internal/config        config.json (secrets generated locally, file ACL'd)
  internal/db            PostgreSQL connection and schema migrations
  internal/store         chats and messages
  internal/policy        settings, approved list, rate limit, attachment rules, audit log
  internal/wa            whatsmeow client, history sync, media, sending, filename sanitizing
  internal/mcptools      MCP tools (19)
  internal/httpapi       HTTP server: network + token guard, /mcp, /api/*
  internal/pgsetup       creates and registers the private PostgreSQL
  internal/sqlitemigrate imports the old messages.db
  internal/proxy         stdio <-> HTTP bridge for Claude Desktop on another machine
installer/               NSIS script and build.sh
docs/GUIA.md             user guide (Spanish)
```

## Build

```
installer/build.sh 2.0.0     # needs Go, makensis, python3, curl
```

Produces `dist/WhatsAppMCP-Setup-2.0.0.exe` plus macOS binaries for the
`connect` helper. The installer bundles PostgreSQL 17 for Windows
(zonky embedded-postgres-binaries, checksum-pinned) and the MSVC runtime DLLs.

## Commands

`whatsapp-mcp help` lists them: `install`, `uninstall [--purge]`, `pair [--phone]`,
`unlink`, `status`, `migrate-sqlite --from messages.db`, `settings [set K V]`,
`allow list|add|remove`, `audit`, `connection-info`, `rotate-token`, `menu`,
`connect --url` (client side), `version`.

## Tests

```
cd whatsapp-bridge
go test ./...                                    # unit tests
WHATSAPP_MCP_TEST_DB=postgres://... go test ./internal/policy   # with a real PostgreSQL
```

## License

MIT, as upstream. whatsmeow is MPL-2.0. See `installer/THIRD-PARTY.txt`.
