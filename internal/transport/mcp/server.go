// Package mcp serves the daemon's capabilities over an MCP streamable-HTTP
// transport mounted at /v1/mcp. Tool handlers call service.Service directly;
// the package adds no new state.
package mcp

import (
	"log/slog"
	"net/http"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/service"
)

// Deps is the bundle the MCP transport needs.
type Deps struct {
	Service service.Service
	Logger  *slog.Logger
	Version string
}

// New returns an http.Handler that speaks MCP over streamable HTTP.
func New(d Deps) http.Handler {
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return newServer(d) },
		nil,
	)
}

func newServer(d Deps) *mcpsdk.Server {
	srv := mcpsdk.NewServer(&mcpsdk.Implementation{
		Name:    "whatsmeow-api",
		Version: d.Version,
	}, &mcpsdk.ServerOptions{
		Instructions: instructions,
	})
	registerStatusTools(srv, d)
	registerChatTools(srv, d)
	registerContactTools(srv, d)
	registerReadOnlyMessageTools(srv, d)
	return srv
}

const instructions = `This server controls a single WhatsApp account through ` +
	`the whatsmeow-api daemon. Chat and group identifiers are WhatsApp JIDs ` +
	`(e.g. "1234567890@s.whatsapp.net" for users, "<group-id>@g.us" for groups). ` +
	`Phone numbers for wa_login_phone are E.164 without the "+". To clear an ` +
	`existing reaction call wa_react with an empty emoji. Messages are ` +
	`searched against the local cache only; remote-only history is not indexed.`
