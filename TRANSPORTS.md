# TRANSPORTS

**The wire is two atoms** — `http_request` (CALL) and `bidi_command` (CHANNEL); every transport reduces to one of them, or is an adapter that presents one.

The full, authoritative transport map is **not duplicated here** — it lives once in the `http-mcp` repo, so it cannot drift:

- **Prose + diagram:** `http-mcp/TRANSPORTS.md`
- **Machine-readable source of truth:** `http-mcp/contract/transports/transports.json` — embedded via `//go:embed`, served verbatim by the http-mcp `transports` MCP tool, and kept in sync with the prose by a CI drift-guard (`contract/transports/transports_prose_test.go`).

> This repo intentionally links out rather than copying the table, so there is nothing here to fall out of sync.
