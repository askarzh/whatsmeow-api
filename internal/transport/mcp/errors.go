package mcp

import (
	"errors"
	"fmt"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

// mapErr converts a service-layer error into an MCP tool error.
// Known error types get a categorised message; everything else is logged in
// full and replaced with a generic "internal error" so the client never sees
// internals (SQL state, file paths, etc.).
//
// Buckets:
//   - service.ErrInvalidRequest → "invalid request: ..." (via CallToolResult, IsError=true)
//   - service.ErrForbidden      → "forbidden: ..."       (via CallToolResult, IsError=true)
//   - store.ErrNotFound         → "not found: ..."       (via CallToolResult, IsError=true)
//   - any other non-nil error   → logged; returned as a Go error from the handler.
//     The SDK then wraps it as IsError=true with "internal error" text (no JSON-RPC error).
func mapErr(err error, logger *slog.Logger) (*mcpsdk.CallToolResult, error) {
	if err == nil {
		return nil, nil
	}
	switch {
	case errors.Is(err, service.ErrInvalidRequest):
		return toolErr(fmt.Sprintf("invalid request: %s", err.Error())), nil
	case errors.Is(err, service.ErrForbidden):
		return toolErr(fmt.Sprintf("forbidden: %s", err.Error())), nil
	case errors.Is(err, store.ErrNotFound):
		return toolErr(fmt.Sprintf("not found: %s", err.Error())), nil
	default:
		if logger != nil {
			logger.Error("mcp tool error", "err", err)
		}
		return nil, fmt.Errorf("internal error")
	}
}

func toolErr(msg string) *mcpsdk.CallToolResult {
	return &mcpsdk.CallToolResult{
		IsError: true,
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: msg}},
	}
}
