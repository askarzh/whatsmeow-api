package http

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
)

// EventsHandler returns the GET /v1/events SSE handler. It resolves a resume
// cursor (Last-Event-ID header preferred, ?since= query fallback), emits a
// synthetic connection.state frame at id 0, replays from events_log via
// SinceSeq, and then live-tails from the broadcaster subscription. A periodic
// :ping comment is sent every heartbeatSeconds (pass 0 to disable). On
// per-subscriber overflow the broadcaster closes the channel; the handler
// writes one terminal event:error frame and returns.
func EventsHandler(svc service.Service, log store.EventsLog, b *sse.Broadcaster, heartbeatSeconds int) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Resolve resume cursor.
		lastSeq, err := resolveLastSeq(r)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		}

		flusher, ok := w.(http.Flusher)
		if !ok {
			WriteProblem(w, http.StatusInternalServerError, "internal", "streaming not supported")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, ":ready\n\n")
		flusher.Flush()

		// Subscribe BEFORE replay so live events queue while we backfill.
		subID, ch := b.Subscribe()
		defer b.Unsubscribe(subID)

		// Synthetic connection.state at id 0 reflecting the daemon's current
		// state. Always written, regardless of resume cursor.
		status, _ := svc.Status(r.Context())
		writeFrame(w, "connection.state", "0", service.BuildConnectionStatePayload(status, ""))
		flusher.Flush()

		// Replay from the resume cursor.
		const replayBatch = 256
		lastReplayedSeq := lastSeq
		for {
			rows, err := log.SinceSeq(r.Context(), lastReplayedSeq, replayBatch)
			if err != nil {
				return
			}
			if len(rows) == 0 {
				break
			}
			for _, row := range rows {
				writeFrame(w, row.Type, strconv.FormatInt(row.Seq, 10), []byte(row.Payload))
				lastReplayedSeq = row.Seq
			}
			flusher.Flush()
			if len(rows) < replayBatch {
				break
			}
		}

		// Live tail with optional heartbeat.
		var heartbeat <-chan time.Time
		if heartbeatSeconds > 0 {
			t := time.NewTicker(time.Duration(heartbeatSeconds) * time.Second)
			defer t.Stop()
			heartbeat = t.C
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case <-heartbeat:
				fmt.Fprint(w, ":ping\n\n")
				flusher.Flush()
			case ev, ok := <-ch:
				if !ok {
					writeFrame(w, "error", "",
						[]byte(`{"v":1,"code":"events.lagged","detail":"subscriber buffer overflowed; reconnect with Last-Event-ID"}`))
					flusher.Flush()
					return
				}
				if ev.Seq <= lastReplayedSeq {
					continue
				}
				writeFrame(w, ev.Kind, strconv.FormatInt(ev.Seq, 10), ev.Payload)
				lastReplayedSeq = ev.Seq
				flusher.Flush()
			}
		}
	})
}

// resolveLastSeq parses the Last-Event-ID header (preferred) or ?since= query
// param. Both must be non-negative integers; missing is OK and returns 0.
func resolveLastSeq(r *http.Request) (int64, error) {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("Last-Event-ID must be a non-negative integer")
		}
		return n, nil
	}
	if v := r.URL.Query().Get("since"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("since must be a non-negative integer")
		}
		return n, nil
	}
	return 0, nil
}

// writeFrame writes one SSE frame: optional id, optional event, then data.
// data is written verbatim — callers pass JSON-encoded bytes.
func writeFrame(w http.ResponseWriter, event, id string, data []byte) {
	if id != "" {
		fmt.Fprintf(w, "id: %s\n", id)
	}
	if event != "" {
		fmt.Fprintf(w, "event: %s\n", event)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
}
