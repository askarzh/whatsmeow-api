package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/transport/sse"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
)

// Emitter appends to events_log and publishes to the broadcaster after each
// successful append. Construct via NewEmitter; safe to call methods on a nil
// pointer (Emit becomes a no-op).
type Emitter struct {
	log         store.EventsLog
	broadcaster *sse.Broadcaster
	logger      *slog.Logger
}

// NewEmitter constructs an Emitter. The broadcaster may be nil — Emit will
// still append, just won't publish.
func NewEmitter(log store.EventsLog, broadcaster *sse.Broadcaster, logger *slog.Logger) *Emitter {
	if logger == nil {
		logger = slog.Default()
	}
	return &Emitter{log: log, broadcaster: broadcaster, logger: logger}
}

// Emit marshals payload to JSON, appends a row to events_log, and on success
// publishes to the broadcaster. If payload is already a []byte it is treated
// as pre-marshaled JSON (the Build*Payload helpers return that shape). Append
// failures are logged at WARN and the publish is skipped so persistence and
// broadcast stay aligned.
func (e *Emitter) Emit(ctx context.Context, kind string, payload any) {
	if e == nil || e.log == nil {
		return
	}
	if e.logger == nil {
		e.logger = slog.Default()
	}
	var body []byte
	switch p := payload.(type) {
	case []byte:
		body = p
	case json.RawMessage:
		body = []byte(p)
	default:
		var err error
		body, err = json.Marshal(payload)
		if err != nil {
			e.logger.Warn("emit: marshal failed", "kind", kind, "err", err)
			return
		}
	}
	seq, err := e.log.Append(ctx, store.EventLogEntry{
		Type:    kind,
		Payload: string(body),
		Time:    time.Now().UTC(),
	})
	if err != nil {
		e.logger.Warn("emit: append failed", "kind", kind, "err", err)
		return
	}
	if e.broadcaster != nil {
		e.broadcaster.Publish(sse.Event{Seq: seq, Kind: kind, Payload: body})
	}
}

const payloadVersion = 1

// BuildMessageReceivedPayload builds the message.received JSON payload. If
// media is non-empty (MessageID set), a "media" object is included.
func BuildMessageReceivedPayload(m waclient.IncomingMessage, media store.MediaRef) []byte {
	out := map[string]any{
		"v":          payloadVersion,
		"message_id": m.ID,
		"chat_jid":   m.ChatJID,
		"sender_jid": m.SenderJID,
		"kind":       m.Kind,
		"body":       m.Body,
		"timestamp":  m.Timestamp.UTC().Format(time.RFC3339),
	}
	if m.PushName != "" {
		out["push_name"] = m.PushName
	}
	if media.MessageID != "" {
		obj := map[string]any{
			"ref":       media.MessageID,
			"mime_type": media.MIME,
			"size":      media.Size,
			"sha256":    media.SHA256,
		}
		if m.Body != "" {
			obj["caption"] = m.Body
		}
		out["media"] = obj
	}
	return mustMarshal(out)
}

// BuildMessageEditedPayload builds the message.edited JSON payload.
func BuildMessageEditedPayload(messageID, chatJID, body string, editedAt time.Time) []byte {
	return mustMarshal(map[string]any{
		"v":          payloadVersion,
		"message_id": messageID,
		"chat_jid":   chatJID,
		"body":       body,
		"edited_at":  editedAt.UTC().Format(time.RFC3339),
	})
}

// BuildMessageDeletedPayload builds the message.deleted JSON payload.
func BuildMessageDeletedPayload(messageID, chatJID string, deletedAt time.Time) []byte {
	return mustMarshal(map[string]any{
		"v":          payloadVersion,
		"message_id": messageID,
		"chat_jid":   chatJID,
		"deleted_at": deletedAt.UTC().Format(time.RFC3339),
	})
}

// BuildReactionReceivedPayload builds the reaction.received JSON payload.
func BuildReactionReceivedPayload(targetMsgID, senderJID, emoji string, ts time.Time) []byte {
	return mustMarshal(map[string]any{
		"v":                 payloadVersion,
		"target_message_id": targetMsgID,
		"sender_jid":        senderJID,
		"emoji":             emoji,
		"timestamp":         ts.UTC().Format(time.RFC3339),
	})
}

// BuildReceiptReceivedPayload builds the receipt.received JSON payload.
func BuildReceiptReceivedPayload(r waclient.IncomingReceipt) []byte {
	return mustMarshal(map[string]any{
		"v":           payloadVersion,
		"message_ids": r.MessageIDs,
		"chat_jid":    r.ChatJID,
		"reader_jid":  r.ReaderJID,
		"type":        r.Type,
		"timestamp":   r.Timestamp.UTC().Format(time.RFC3339),
	})
}

// BuildTypingReceivedPayload builds the typing.received JSON payload.
func BuildTypingReceivedPayload(chatJID, senderJID, state string, ts time.Time) []byte {
	return mustMarshal(map[string]any{
		"v":          payloadVersion,
		"chat_jid":   chatJID,
		"sender_jid": senderJID,
		"state":      state,
		"timestamp":  ts.UTC().Format(time.RFC3339),
	})
}

// BuildConnectionStatePayload builds the connection.state JSON. Reason is
// included only when connected is false and reason is non-empty.
func BuildConnectionStatePayload(s waclient.Status, reason string) []byte {
	out := map[string]any{
		"v":         payloadVersion,
		"connected": s.Connected,
	}
	if s.JID != nil {
		out["jid"] = *s.JID
	}
	if s.Since != nil {
		out["since"] = s.Since.UTC().Format(time.RFC3339)
	}
	if !s.Connected && reason != "" {
		out["reason"] = reason
	}
	return mustMarshal(out)
}

// mustMarshal marshals the given value to JSON. Build helpers only marshal
// map[string]any payloads, which json.Marshal cannot fail on, so the error
// is intentionally discarded.
func mustMarshal(v any) []byte {
	b, _ := json.Marshal(v)
	return b
}
