package mcp

import (
	"errors"
	"fmt"
	"log/slog"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
)

// mapErr converts a service-layer error into either an MCP "tool error"
// (CallToolResult with IsError=true) or a transport-level error the SDK turns
// into a JSON-RPC internal-error reply.
//
// Buckets:
//   - service.ErrInvalidRequest → tool error "invalid request: ..."
//   - service.ErrForbidden      → tool error "forbidden: ..."
//   - store.ErrNotFound         → tool error "not found: ..."
//   - any other non-nil error   → transport error (logged; client sees "internal error")
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
