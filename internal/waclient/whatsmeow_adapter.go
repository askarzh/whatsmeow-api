package waclient

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	// Database drivers — imported for side-effects so database/sql can find them.
	_ "github.com/jackc/pgx/v5/stdlib"
	_ "modernc.org/sqlite"
)

// Adapter is the production WAClient backed by *whatsmeow.Client.
type Adapter struct {
	container *sqlstore.Container
	logger    *slog.Logger

	mu              sync.Mutex
	client          *whatsmeow.Client
	loginInProgress bool
	lastConnectedAt time.Time

	pairCh          chan string          // signaled with outcome by event handler during phone pair
	incomingHandler func(IncomingMessage) // Plan 04
}

// NewAdapter constructs an Adapter. The container must already be initialized
// (use OpenSQLite or OpenPostgres).
func NewAdapter(container *sqlstore.Container, logger *slog.Logger) *Adapter {
	return &Adapter{container: container, logger: logger}
}

// OpenSQLite opens (or creates) the whatsmeow session DB at path.
func OpenSQLite(ctx context.Context, path string, logger *slog.Logger) (*sqlstore.Container, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)", path)
	c, err := sqlstore.New(ctx, "sqlite", dsn, nil)
	if err != nil {
		return nil, fmt.Errorf("sqlstore sqlite: %w", err)
	}
	return c, nil
}

// OpenPostgres opens a Postgres-backed session store using a dedicated schema.
// dsn is the canonical Postgres URL; the schema "whatsmeow_session" is created
// if missing and search_path is scoped to it for this connection.
func OpenPostgres(ctx context.Context, dsn string, logger *slog.Logger) (*sqlstore.Container, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	if _, err := db.ExecContext(ctx, `CREATE SCHEMA IF NOT EXISTS whatsmeow_session`); err != nil {
		return nil, fmt.Errorf("create schema: %w", err)
	}
	if _, err := db.ExecContext(ctx, `SET search_path TO whatsmeow_session, public`); err != nil {
		return nil, fmt.Errorf("set search_path: %w", err)
	}
	c := sqlstore.NewWithDB(db, "postgres", nil)
	if err := c.Upgrade(ctx); err != nil {
		return nil, fmt.Errorf("sqlstore postgres upgrade: %w", err)
	}
	return c, nil
}

// ensureClient lazily constructs the whatsmeow.Client backed by the existing
// device (or a fresh one if no session is stored).
func (a *Adapter) ensureClient(ctx context.Context) error {
	if a.client != nil {
		return nil
	}
	device, err := a.container.GetFirstDevice(ctx)
	if err != nil {
		return fmt.Errorf("get device: %w", err)
	}
	a.client = whatsmeow.NewClient(device, nil)
	a.client.AddEventHandler(a.onEvent)
	return nil
}

// onEvent is the single subscription point for whatsmeow events.
func (a *Adapter) onEvent(raw any) {
	switch evt := raw.(type) {
	case *events.Connected:
		a.mu.Lock()
		a.lastConnectedAt = time.Now()
		a.mu.Unlock()
		a.logger.Info("wa connected")
	case *events.Disconnected:
		a.logger.Info("wa disconnected")
	case *events.LoggedOut:
		a.logger.Info("wa logged out", "reason", evt.Reason.String())
	case *events.PairSuccess:
		a.logger.Info("wa pair success", "jid", evt.ID.String())
		a.signalPair("success")
	case *events.PairError:
		a.logger.Warn("wa pair error", "err", evt.Error.Error())
		a.signalPair("err-" + evt.Error.Error())
	case *events.Message:
		incoming, ok := translateIncoming(a, evt)
		if !ok {
			return // protocol/system message; skip
		}
		a.mu.Lock()
		h := a.incomingHandler
		a.mu.Unlock()
		if h != nil {
			h(incoming)
		}
	}
}

func (a *Adapter) signalPair(outcome string) {
	a.mu.Lock()
	ch := a.pairCh
	a.pairCh = nil
	a.mu.Unlock()
	if ch != nil {
		select {
		case ch <- outcome:
		default:
		}
	}
}

// translateIncoming converts a whatsmeow events.Message into our domain type.
// Returns (_, false) for protocol/system events that have no text or media body,
// and for self-sent (echo) messages so outgoing sends don't increment unread counts.
func translateIncoming(a *Adapter, evt *events.Message) (IncomingMessage, bool) {
	if evt.Info.IsFromMe {
		return IncomingMessage{}, false
	}
	if evt.Message != nil && evt.Message.ProtocolMessage != nil {
		return translateProtocol(evt)
	}
	kind, body, downloader, ok := messageKindAndBody(a, evt.Message)
	if !ok {
		return IncomingMessage{}, false
	}
	return IncomingMessage{
		ID:              evt.Info.ID,
		ChatJID:         evt.Info.Chat.String(),
		ChatKind:        ChatKindFromJID(evt.Info.Chat.String()),
		SenderJID:       evt.Info.Sender.String(),
		Timestamp:       evt.Info.Timestamp,
		Kind:            kind,
		Body:            body,
		PushName:        evt.Info.PushName,
		MediaDownloader: downloader,
	}, true
}

// translateProtocol handles inbound *waE2E.ProtocolMessage events for revoke +
// edit. Returns false for protocol-message types we don't handle in Plan 07a
// (e.g. read receipts arrive separately).
func translateProtocol(evt *events.Message) (IncomingMessage, bool) {
	pm := evt.Message.ProtocolMessage
	switch pm.GetType() {
	case waE2E.ProtocolMessage_REVOKE:
		return IncomingMessage{
			ID:         evt.Info.ID,
			ChatJID:    evt.Info.Chat.String(),
			ChatKind:   ChatKindFromJID(evt.Info.Chat.String()),
			SenderJID:  evt.Info.Sender.String(),
			Timestamp:  evt.Info.Timestamp,
			RevokeOfID: pm.GetKey().GetID(),
		}, true
	case waE2E.ProtocolMessage_MESSAGE_EDIT:
		body := ""
		if edited := pm.GetEditedMessage(); edited != nil {
			body = edited.GetConversation()
		}
		return IncomingMessage{
			ID:        evt.Info.ID,
			ChatJID:   evt.Info.Chat.String(),
			ChatKind:  ChatKindFromJID(evt.Info.Chat.String()),
			SenderJID: evt.Info.Sender.String(),
			Timestamp: evt.Info.Timestamp,
			Body:      body,
			EditOfID:  pm.GetKey().GetID(),
		}, true
	default:
		return IncomingMessage{}, false
	}
}

// messageKindAndBody picks the relevant field out of a *waE2E.Message and
// returns kind, text body (text kinds only), an optional downloader closure
// (media kinds only), and a `false` for variants we don't persist.
func messageKindAndBody(a *Adapter, m *waE2E.Message) (string, string, func(context.Context) ([]byte, string, error), bool) {
	if m == nil {
		return "", "", nil, false
	}
	switch {
	case m.Conversation != nil:
		return "text", *m.Conversation, nil, true
	case m.ExtendedTextMessage != nil && m.ExtendedTextMessage.Text != nil:
		return "text", *m.ExtendedTextMessage.Text, nil, true
	case m.ImageMessage != nil:
		img := m.ImageMessage
		return "image", "", func(ctx context.Context) ([]byte, string, error) {
			body, err := a.client.Download(ctx, img)
			if err != nil {
				return nil, "", err
			}
			return body, img.GetMimetype(), nil
		}, true
	case m.VideoMessage != nil:
		vid := m.VideoMessage
		return "video", "", func(ctx context.Context) ([]byte, string, error) {
			body, err := a.client.Download(ctx, vid)
			if err != nil {
				return nil, "", err
			}
			return body, vid.GetMimetype(), nil
		}, true
	case m.AudioMessage != nil:
		aud := m.AudioMessage
		return "audio", "", func(ctx context.Context) ([]byte, string, error) {
			body, err := a.client.Download(ctx, aud)
			if err != nil {
				return nil, "", err
			}
			return body, aud.GetMimetype(), nil
		}, true
	case m.DocumentMessage != nil:
		doc := m.DocumentMessage
		return "document", "", func(ctx context.Context) ([]byte, string, error) {
			body, err := a.client.Download(ctx, doc)
			if err != nil {
				return nil, "", err
			}
			return body, doc.GetMimetype(), nil
		}, true
	case m.StickerMessage != nil:
		stk := m.StickerMessage
		return "sticker", "", func(ctx context.Context) ([]byte, string, error) {
			body, err := a.client.Download(ctx, stk)
			if err != nil {
				return nil, "", err
			}
			return body, stk.GetMimetype(), nil
		}, true
	default:
		return "", "", nil, false
	}
}

// Status returns the current view of the connection.
func (a *Adapter) Status() Status {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil ||
		!a.client.IsConnected() || !a.client.IsLoggedIn() {
		return Status{}
	}
	jid := a.client.Store.ID.String()
	push := a.client.Store.PushName
	since := a.lastConnectedAt
	return Status{
		Connected: true,
		JID:       &jid,
		PushName:  &push,
		Since:     &since,
	}
}

// Resume connects an existing session if one is stored. No-op if none.
func (a *Adapter) Resume(ctx context.Context) error {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return err
	}
	if a.client.Store.ID == nil {
		a.mu.Unlock()
		return nil // no saved session
	}
	a.mu.Unlock()
	if err := a.client.Connect(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	return nil
}

// LoginQR starts a fresh QR pairing flow.
func (a *Adapter) LoginQR(ctx context.Context) (<-chan QREvent, error) {
	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.mu.Unlock()

	qrCh, err := a.client.GetQRChannel(ctx)
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("get qr channel: %w", err)
	}
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}

	out := make(chan QREvent, 4)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.mu.Unlock()
		}()
		for evt := range qrCh {
			switch evt.Event {
			case "code":
				select {
				case out <- QREvent{Code: evt.Code}:
				case <-ctx.Done():
					out <- QREvent{Terminal: true, Outcome: "ctx-cancelled"}
					return
				}
			case "success":
				out <- QREvent{Terminal: true, Outcome: "success"}
				return
			case "timeout":
				out <- QREvent{Terminal: true, Outcome: "timeout"}
				return
			default:
				out <- QREvent{Terminal: true, Outcome: "err-" + evt.Event}
				return
			}
		}
	}()
	return out, nil
}

// LoginPhone starts a phone-pair flow. The first PairEvent carries the 8-char
// pair code; the terminal event arrives when the user enters the code (or the
// 2-minute window expires).
func (a *Adapter) LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error) {
	if !IsValidPhoneNumber(phoneNumber) {
		return nil, fmt.Errorf("invalid phone number")
	}

	a.mu.Lock()
	if err := a.ensureClient(ctx); err != nil {
		a.mu.Unlock()
		return nil, err
	}
	if a.client.Store.ID != nil && a.client.IsLoggedIn() {
		a.mu.Unlock()
		return nil, ErrAlreadyLoggedIn
	}
	if a.loginInProgress {
		a.mu.Unlock()
		return nil, ErrLoginInProgress
	}
	a.loginInProgress = true
	a.pairCh = make(chan string, 1)
	pairCh := a.pairCh
	a.mu.Unlock()

	// Connect first so PairPhone can send the linking request.
	if err := a.client.Connect(); err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("connect: %w", err)
	}
	code, err := a.client.PairPhone(ctx, phoneNumber, true, whatsmeow.PairClientChrome, "Chrome (Linux)")
	if err != nil {
		a.mu.Lock()
		a.loginInProgress = false
		a.pairCh = nil
		a.mu.Unlock()
		return nil, fmt.Errorf("pair phone: %w", err)
	}

	out := make(chan PairEvent, 2)
	go func() {
		defer close(out)
		defer func() {
			a.mu.Lock()
			a.loginInProgress = false
			a.pairCh = nil
			a.mu.Unlock()
		}()
		out <- PairEvent{Code: code}
		select {
		case outcome := <-pairCh:
			out <- PairEvent{Terminal: true, Outcome: outcome}
		case <-ctx.Done():
			out <- PairEvent{Terminal: true, Outcome: "ctx-cancelled"}
		case <-time.After(150 * time.Second):
			out <- PairEvent{Terminal: true, Outcome: "timeout"}
		}
	}()
	return out, nil
}

// Logout disconnects and tells WhatsApp to invalidate the session.
func (a *Adapter) Logout(ctx context.Context) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client == nil || a.client.Store == nil || a.client.Store.ID == nil || !a.client.IsLoggedIn() {
		return ErrNotLoggedIn
	}
	if err := a.client.Logout(ctx); err != nil {
		return fmt.Errorf("logout: %w", err)
	}
	return nil
}

// Close disconnects without logging out. Called on daemon shutdown.
func (a *Adapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.client != nil && a.client.IsConnected() {
		a.client.Disconnect()
	}
	return nil
}

// SendText sends a plain-text message to chatJID.
// replyTo, if non-empty, is the message ID this is a reply to.
func (a *Adapter) SendText(ctx context.Context, chatJID, text, replyTo string) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}

	var msg *waE2E.Message
	if replyTo == "" {
		msg = &waE2E.Message{Conversation: proto.String(text)}
	} else {
		msg = &waE2E.Message{
			ExtendedTextMessage: &waE2E.ExtendedTextMessage{
				Text: proto.String(text),
				ContextInfo: &waE2E.ContextInfo{
					StanzaID: proto.String(replyTo),
				},
			},
		}
	}

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send text: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}

// OnIncomingMessage registers a handler invoked once per incoming message
// event, after translation into the domain type IncomingMessage. Setting nil
// clears the handler. Calling this twice replaces the previous handler.
func (a *Adapter) OnIncomingMessage(handler func(IncomingMessage)) {
	a.mu.Lock()
	a.incomingHandler = handler
	a.mu.Unlock()
}

// SendMedia uploads body to WhatsApp's media servers, builds the appropriate
// proto message for the kind ("image" or "document"), and sends it to chatJID.
func (a *Adapter) SendMedia(ctx context.Context, chatJID, kind, caption, filename, mime string, body []byte) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}

	var mediaType whatsmeow.MediaType
	switch kind {
	case "image":
		mediaType = whatsmeow.MediaImage
	case "document":
		mediaType = whatsmeow.MediaDocument
	default:
		return Sent{}, fmt.Errorf("unsupported media kind: %q", kind)
	}

	upload, err := client.Upload(ctx, body, mediaType)
	if err != nil {
		return Sent{}, fmt.Errorf("upload: %w", err)
	}

	msg := buildMediaProto(kind, caption, filename, mime, body, upload)
	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send media: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}

// buildMediaProto constructs the *waE2E.Message variant for the given kind.
func buildMediaProto(kind, caption, filename, mime string, body []byte, upload whatsmeow.UploadResponse) *waE2E.Message {
	length := uint64(len(body))
	switch kind {
	case "image":
		return &waE2E.Message{
			ImageMessage: &waE2E.ImageMessage{
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				Mimetype:      proto.String(mime),
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(length),
				Caption:       optionalString(caption),
			},
		}
	case "document":
		return &waE2E.Message{
			DocumentMessage: &waE2E.DocumentMessage{
				URL:           proto.String(upload.URL),
				DirectPath:    proto.String(upload.DirectPath),
				MediaKey:      upload.MediaKey,
				Mimetype:      proto.String(mime),
				FileEncSHA256: upload.FileEncSHA256,
				FileSHA256:    upload.FileSHA256,
				FileLength:    proto.Uint64(length),
				Title:         proto.String(filename),
				FileName:      proto.String(filename),
				Caption:       optionalString(caption),
			},
		}
	default:
		return nil
	}
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}

// SendEdit sends a MESSAGE_EDIT ProtocolMessage targeting the given message id.
// Only owner-edits succeed (whatsmeow rejects edits to messages we didn't send).
func (a *Adapter) SendEdit(ctx context.Context, chatJID, originalMessageID, newText string) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}

	msg := client.BuildEdit(to, originalMessageID, &waE2E.Message{
		Conversation: proto.String(newText),
	})

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send edit: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}

// SendRevoke sends a REVOKE ProtocolMessage targeting the given message id.
// Passing types.EmptyJID as the sender revokes one of our own messages.
func (a *Adapter) SendRevoke(ctx context.Context, chatJID, originalMessageID string) (Sent, error) {
	a.mu.Lock()
	if a.client == nil || !a.client.IsConnected() || !a.client.IsLoggedIn() {
		a.mu.Unlock()
		return Sent{}, ErrNotConnected
	}
	senderJID := a.client.Store.ID.String()
	client := a.client
	a.mu.Unlock()

	to, err := types.ParseJID(chatJID)
	if err != nil {
		return Sent{}, fmt.Errorf("parse chat_jid: %w", err)
	}

	// types.EmptyJID signals "my own message" to BuildRevoke.
	msg := client.BuildRevoke(to, types.EmptyJID, originalMessageID)

	resp, err := client.SendMessage(ctx, to, msg)
	if err != nil {
		return Sent{}, fmt.Errorf("send revoke: %w", err)
	}
	return Sent{
		ID:        resp.ID,
		Timestamp: resp.Timestamp,
		SenderJID: senderJID,
	}, nil
}

// compile-time interface check
var _ WAClient = (*Adapter)(nil)
