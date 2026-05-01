package http

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// SSEPrepare sets the standard SSE response headers. Call once at the start of
// a handler before any SSEWrite* call.
func SSEPrepare(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
}

// SSEWriteEvent writes one Server-Sent Event frame: "event: <name>\ndata: <json>\n\n".
// The payload is encoded as JSON. If the writer implements http.Flusher, the
// frame is flushed immediately so clients see it without buffering.
func SSEWriteEvent(w http.ResponseWriter, name string, payload any) error {
	buf, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, buf); err != nil {
		return fmt.Errorf("write event: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}

// SSEWriteHeartbeat writes a comment-only frame, used to keep proxies from
// closing idle SSE connections.
func SSEWriteHeartbeat(w http.ResponseWriter) error {
	if _, err := fmt.Fprint(w, ": heartbeat\n\n"); err != nil {
		return fmt.Errorf("write heartbeat: %w", err)
	}
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	return nil
}
