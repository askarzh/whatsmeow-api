# whatsmeow-api Plan 06 — Media Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add media handling: inbound auto-download (all 5 kinds), outbound `POST /v1/media` for image + document, and `GET /v1/media/{message_id}` to stream stored bytes. Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>`.

**Architecture:** New `internal/mediastore` package owns disk IO. waclient.IncomingMessage gains a `MediaDownloader` closure populated by the adapter; service spawns a goroutine per incoming media message to call the closure + persist bytes. Outbound goes through a new `WAClient.SendMedia` (uses whatsmeow's `Client.Upload` + builds proto + `SendMessage`). HTTP handler parses multipart and delegates to service.

**Tech Stack:**
- Go 1.26
- Plan 01–05 stack (chi, cobra, koanf, slog, testify, modernc.org/sqlite, golang-migrate)
- `mime/multipart` (stdlib)
- whatsmeow `Client.Upload`, `Client.Download`, `MediaImage`/`MediaDocument`, `*waE2E.{Image,Document,Video,Audio,Sticker}Message`

---

## File Structure

| Path | Responsibility |
|---|---|
| `internal/config/config.go` | Modified — add `HTTPConfig{MaxBodyBytes int64}`. |
| `internal/config/config_test.go` | Modified — extend defaults test. |
| `config.example.toml` | Modified — `[http] max_body_bytes`. |
| `internal/mediastore/mediastore.go` | NEW — `Store`, `Write`, `Path`, `ExtFromMIME`. |
| `internal/mediastore/mediastore_test.go` | NEW — round-trip + idempotency + ext mapping. |
| `internal/waclient/waclient.go` | Modified — `IncomingMessage.MediaDownloader`; `SendMedia` in interface. |
| `internal/waclient/whatsmeow_adapter.go` | Modified — `SendMedia` impl; `MediaDownloader` closures in `translateIncoming`. |
| `internal/service/service.go` | Modified — `SendMediaRequest`, `SendMedia`, `GetMediaRef`, `handleIncoming` download goroutine, `New` takes `*mediastore.Store`. |
| `internal/service/service_test.go` | Modified — extend bundle helper + tests. |
| `internal/transport/http/media.go` | NEW — `SendMediaHandler`, `GetMediaHandler`. |
| `internal/transport/http/media_test.go` | NEW. |
| `internal/transport/http/router.go` | Modified — 2 routes. |
| `cmd/whatsmeow-api/serve.go` | Modified — construct `mediastore.Store`, pass to `service.New`. |
| `README.md` | Modified — status section. |

No new dependencies, no new schema migrations.

---

## Task 1: HTTPConfig.MaxBodyBytes

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/config_test.go`
- Modify: `config.example.toml`

- [ ] **Step 1: Inspect the existing Config struct**

```bash
cd /home/askar/src/whatsmeow-api
grep -n "type Config\|type ServerConfig\|type AuthConfig" internal/config/config.go
```

You'll see the existing top-level `Config` with sub-structs. Plan 06 adds an `HTTP HTTPConfig` field at the top level.

- [ ] **Step 2: Update the failing config test**

Edit `internal/config/config_test.go`. Find the existing defaults test (looks for `TestLoadDefaults` or similar). Add an assertion:

```go
assert.Equal(t, int64(100*1024*1024), c.HTTP.MaxBodyBytes)
```

at a suitable place inside the defaults test.

- [ ] **Step 3: Run failing test**

```bash
go test ./internal/config/...
```

Expected: FAIL — `c.HTTP.MaxBodyBytes` undefined.

- [ ] **Step 4: Add HTTPConfig and default**

Edit `internal/config/config.go`. Add the new struct near the other sub-structs:
```go
type HTTPConfig struct {
	MaxBodyBytes int64 `koanf:"max_body_bytes"`
}
```

Add the field to the top-level `Config`:
```go
type Config struct {
	DataDir string        `koanf:"data_dir"`
	Server  ServerConfig  `koanf:"server"`
	Auth    AuthConfig    `koanf:"auth"`
	Storage StorageConfig `koanf:"storage"`
	Log     LogConfig     `koanf:"log"`
	Events  EventsConfig  `koanf:"events"`
	Metrics MetricsConfig `koanf:"metrics"`
	HTTP    HTTPConfig    `koanf:"http"` // NEW
}
```

Find the `Load` function (or `applyDefaults` helper) where other defaults are set. Add:
```go
if c.HTTP.MaxBodyBytes == 0 {
	c.HTTP.MaxBodyBytes = 100 * 1024 * 1024 // 100 MB
}
```

(Place this next to the other `if c.X == "" { c.X = ... }` lines.)

- [ ] **Step 5: Update config.example.toml**

Append a section:
```toml
[http]
# Maximum body size for POST /v1/media (uploads). Default 100 MiB.
max_body_bytes = 104857600
```

- [ ] **Step 6: Run tests**

```bash
go test ./internal/config/... -v
go test ./...
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/config_test.go config.example.toml
git commit -m "config: add http.max_body_bytes (default 100 MiB)"
```

---

## Task 2: mediastore package

**Files:**
- Create: `internal/mediastore/mediastore.go`
- Create: `internal/mediastore/mediastore_test.go`

Pure utility package — no deps on the rest of the codebase. Standalone tests via `t.TempDir()`.

- [ ] **Step 1: Write the failing test**

Create `internal/mediastore/mediastore_test.go`:
```go
package mediastore_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/askarzh/whatsmeow-api/internal/mediastore"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtFromMIME(t *testing.T) {
	cases := []struct{ mime, want string }{
		{"image/jpeg", ".jpg"},
		{"image/png", ".png"},
		{"image/webp", ".webp"},
		{"image/gif", ".gif"},
		{"video/mp4", ".mp4"},
		{"audio/ogg", ".ogg"},
		{"audio/mpeg", ".mp3"},
		{"application/pdf", ".pdf"},
		{"application/zip", ".zip"},
		{"application/octet-stream", ".bin"},
		{"unknown/type", ".bin"},
		{"", ".bin"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			assert.Equal(t, tc.want, mediastore.ExtFromMIME(tc.mime))
		})
	}
}

func TestPath(t *testing.T) {
	s := mediastore.New("/tmp/root")
	got := s.Path("abcdef0123456789", ".jpg")
	assert.Equal(t, "/tmp/root/ab/abcdef0123456789.jpg", got)
}

func TestWriteRoundTrip(t *testing.T) {
	root := t.TempDir()
	s := mediastore.New(root)
	body := []byte("hello world media bytes")
	mime := "image/jpeg"

	sha, path, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)

	// sha is the lowercase-hex sha256 of body
	expSum := sha256.Sum256(body)
	expHex := hex.EncodeToString(expSum[:])
	assert.Equal(t, expHex, sha)

	// path follows <root>/<sha[0:2]>/<sha>.jpg
	assert.Equal(t, filepath.Join(root, sha[0:2], sha+".jpg"), path)

	// file contents match
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	assert.Equal(t, body, got)
}

func TestWriteIdempotent(t *testing.T) {
	root := t.TempDir()
	s := mediastore.New(root)
	body := []byte("same bytes twice")
	mime := "image/png"

	sha1, path1, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)
	st1, err := os.Stat(path1)
	require.NoError(t, err)
	mtime1 := st1.ModTime()

	// second write: same bytes → same sha + path; should not rewrite the file.
	sha2, path2, err := s.Write(context.Background(), body, mime)
	require.NoError(t, err)
	assert.Equal(t, sha1, sha2)
	assert.Equal(t, path1, path2)

	st2, err := os.Stat(path2)
	require.NoError(t, err)
	assert.Equal(t, mtime1, st2.ModTime(), "second write should be a no-op")
}

func TestWriteCreatesParentDir(t *testing.T) {
	// Root doesn't exist yet.
	root := filepath.Join(t.TempDir(), "nested", "deeper", "media")
	s := mediastore.New(root)
	_, _, err := s.Write(context.Background(), []byte("x"), "image/jpeg")
	require.NoError(t, err, "Write must MkdirAll the parent dir")
}
```

- [ ] **Step 2: Run failing test**

```bash
go test ./internal/mediastore/...
```

Expected: FAIL — package doesn't exist.

- [ ] **Step 3: Implement the package**

Create `internal/mediastore/mediastore.go`:
```go
// Package mediastore owns on-disk media files for the daemon. Files are
// content-addressed by SHA-256: <root>/<sha[0:2]>/<sha>.<ext>. Identical
// bytes uploaded by two different messages share one disk file.
package mediastore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
)

type Store struct {
	root string
}

// New constructs a Store rooted at root. The directory tree is created lazily
// on first Write.
func New(root string) *Store {
	return &Store{root: root}
}

// Write hashes body, derives the on-disk path from sha + ExtFromMIME(mime),
// ensures the parent dir exists, and atomically writes the file via a .tmp
// sibling + rename. If the destination file already exists at the right size,
// no rewrite (idempotent on re-upload of identical bytes).
//
// Returns (sha256-hex, full-path, error).
func (s *Store) Write(ctx context.Context, body []byte, mime string) (string, string, error) {
	sum := sha256.Sum256(body)
	sha := hex.EncodeToString(sum[:])
	ext := ExtFromMIME(mime)
	path := s.Path(sha, ext)

	// If the file already exists with the expected size, treat as no-op.
	if st, err := os.Stat(path); err == nil && st.Size() == int64(len(body)) {
		return sha, path, nil
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", "", fmt.Errorf("mediastore mkdir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, body, 0o640); err != nil {
		return "", "", fmt.Errorf("mediastore write tmp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", "", fmt.Errorf("mediastore rename: %w", err)
	}
	return sha, path, nil
}

// Path returns the on-disk path for a given sha + extension. No IO.
func (s *Store) Path(sha, ext string) string {
	prefix := ""
	if len(sha) >= 2 {
		prefix = sha[:2]
	}
	return filepath.Join(s.root, prefix, sha+ext)
}

// ExtFromMIME maps a MIME type to a file extension (with leading dot).
// Unknown / empty types return ".bin".
func ExtFromMIME(mime string) string {
	switch mime {
	case "image/jpeg", "image/jpg":
		return ".jpg"
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	case "image/heic":
		return ".heic"
	case "video/mp4":
		return ".mp4"
	case "video/quicktime":
		return ".mov"
	case "video/webm":
		return ".webm"
	case "audio/ogg", "audio/ogg; codecs=opus":
		return ".ogg"
	case "audio/mpeg":
		return ".mp3"
	case "audio/aac":
		return ".aac"
	case "audio/wav":
		return ".wav"
	case "application/pdf":
		return ".pdf"
	case "application/zip":
		return ".zip"
	case "application/x-tar":
		return ".tar"
	case "text/plain":
		return ".txt"
	default:
		return ".bin"
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/mediastore/... -v
```

Expected: all 4 tests PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/mediastore/
git commit -m "mediastore: content-addressed on-disk media storage"
```

---

## Task 3: waclient types + adapter stubs

**Files:**
- Modify: `internal/waclient/waclient.go`
- Modify: `internal/waclient/whatsmeow_adapter.go`
- Modify: `internal/service/service_test.go` (fakeWA stub)

Adds the new `MediaDownloader` field, the new `SendMedia` interface method, and stubs in the adapter so the build stays green. Tasks 4 + 5 fill in the real implementations.

- [ ] **Step 1: Extend IncomingMessage and the WAClient interface**

Edit `internal/waclient/waclient.go`. Find `type IncomingMessage struct` and append a field at the bottom:
```go
type IncomingMessage struct {
	ID        string
	ChatJID   string
	ChatKind  string
	SenderJID string
	Timestamp time.Time
	Kind      string
	Body      string
	PushName  string
	// Plan 06: closure that downloads the media bytes for this message.
	// nil for non-media kinds. The adapter populates it with a closure that
	// captures the *waE2E submessage; service calls it from a goroutine.
	MediaDownloader func(ctx context.Context) ([]byte, string /* mime */, error)
}
```

Find `type WAClient interface` and add `SendMedia`:
```go
type WAClient interface {
	// Plan 02 surface
	Status() Status
	Resume(ctx context.Context) error
	LoginQR(ctx context.Context) (<-chan QREvent, error)
	LoginPhone(ctx context.Context, phoneNumber string) (<-chan PairEvent, error)
	Logout(ctx context.Context) error
	Close() error

	// Plan 04
	SendText(ctx context.Context, chatJID, text string) (Sent, error)
	OnIncomingMessage(handler func(IncomingMessage))

	// Plan 06
	SendMedia(ctx context.Context, chatJID, kind, caption, filename, mime string, body []byte) (Sent, error)
}
```

- [ ] **Step 2: Add adapter stub for SendMedia**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the bottom of the file (just before `var _ WAClient = (*Adapter)(nil)`). Add a stub:
```go
// SendMedia is implemented in Task 4.
func (a *Adapter) SendMedia(ctx context.Context, chatJID, kind, caption, filename, mime string, body []byte) (Sent, error) {
	_ = ctx
	_ = chatJID
	_ = kind
	_ = caption
	_ = filename
	_ = mime
	_ = body
	return Sent{}, errors.New("waclient: SendMedia not yet implemented")
}
```

Add `"errors"` to the import block of the adapter if not already present. (Plan 04 had it but Task 2 of Plan 04 may have removed it.)

- [ ] **Step 3: Bridge the in-memory fakeWA in service_test.go**

Edit `internal/service/service_test.go`. Find `type fakeWA struct` and the existing methods. Add a stub for SendMedia:
```go
func (f *fakeWA) SendMedia(context.Context, string, string, string, string, string, []byte) (waclient.Sent, error) {
	return waclient.Sent{}, nil
}
```

- [ ] **Step 4: Build, vet, run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: clean. Existing tests still pass; no new tests at this stage.

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/waclient.go internal/waclient/whatsmeow_adapter.go internal/service/service_test.go
git commit -m "waclient: add MediaDownloader + SendMedia interface (stubs)"
```

---

## Task 4: waclient.SendMedia adapter implementation

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

No automated test (real WhatsApp). Manual smoke (Task 10) covers it.

- [ ] **Step 1: Inspect whatsmeow APIs**

```bash
go doc go.mau.fi/whatsmeow.Client.Upload
go doc go.mau.fi/whatsmeow.UploadResponse
go doc go.mau.fi/whatsmeow.MediaType
go doc go.mau.fi/whatsmeow/proto/waE2E.ImageMessage
go doc go.mau.fi/whatsmeow/proto/waE2E.DocumentMessage
```

Confirm:
- `Client.Upload(ctx context.Context, plaintext []byte, appInfo MediaType) (UploadResponse, error)` (signature may differ slightly — adapt as needed)
- `UploadResponse` exposes `URL`, `DirectPath`, `MediaKey`, `FileEncSHA256`, `FileSHA256`, `FileLength`
- `MediaImage`, `MediaDocument` are `MediaType` constants
- `*waE2E.ImageMessage` has fields: `URL *string`, `Mimetype *string`, `Caption *string`, `FileSHA256 []byte`, `FileEncSHA256 []byte`, `FileLength *uint64`, `MediaKey []byte`, `DirectPath *string`
- `*waE2E.DocumentMessage` adds `Title *string`, `FileName *string`

If field names differ from these, adapt — the intent is "upload bytes, build proto from upload metadata + caption/filename/mime, send."

- [ ] **Step 2: Replace the SendMedia stub**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the stub from Task 3 and replace its body:

```go
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
		return Sent{}, fmt.Errorf("unsupported media kind: %q (Plan 06 supports image, document)", kind)
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
		// SendMedia rejects unsupported kinds before reaching this point.
		return nil
	}
}

func optionalString(s string) *string {
	if s == "" {
		return nil
	}
	return proto.String(s)
}
```

`whatsmeow`, `types`, `waE2E`, `proto`, and `fmt` should already be imported from Plan 04's Task 2. Verify and add any missing imports.

- [ ] **Step 3: Build and vet**

```bash
go build ./...
go vet ./...
```

Expected: clean. If you get whatsmeow API mismatches, run `go doc` on the offending symbol and adapt.

- [ ] **Step 4: Run all tests**

```bash
go test ./... -race
```

Expected: PASS. No new automated test for SendMedia (interface compliance is checked by the existing `var _ WAClient = (*Adapter)(nil)`).

- [ ] **Step 5: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: implement SendMedia (image + document)"
```

---

## Task 5: waclient inbound MediaDownloader closures

**Files:**
- Modify: `internal/waclient/whatsmeow_adapter.go`

Extends `translateIncoming` to populate `IncomingMessage.MediaDownloader` for media kinds. No automated test; manual smoke (Task 10) covers it.

- [ ] **Step 1: Inspect whatsmeow Download**

```bash
go doc go.mau.fi/whatsmeow.Client.Download
go doc go.mau.fi/whatsmeow.DownloadableMessage
```

Confirm: `Client.Download(ctx context.Context, msg DownloadableMessage) ([]byte, error)`. `*waE2E.ImageMessage`, `*waE2E.VideoMessage`, etc. all implement `DownloadableMessage`. (Adapter signatures may differ — adapt if needed.)

- [ ] **Step 2: Update translateIncoming + messageKindAndBody**

Edit `internal/waclient/whatsmeow_adapter.go`. Find the existing helpers `translateIncoming` and `messageKindAndBody`. The current shape returns `(kind, body, hasBody)`. We need to ALSO return a downloader closure for media kinds.

Refactor so `messageKindAndBody` returns `(kind, body, downloader, hasBody)`. Adapt callers accordingly.

Replace the existing helpers with:
```go
// translateIncoming converts a whatsmeow events.Message into our domain type.
// Returns (_, false) for protocol/system events that have no text or media body,
// and for self-sent echoes (handled in Plan 04's IsFromMe filter).
func translateIncoming(a *Adapter, evt *events.Message) (IncomingMessage, bool) {
	if evt.Info.IsFromMe {
		return IncomingMessage{}, false
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
```

The existing `onEvent` switch case `*events.Message` calls `translateIncoming(evt)`. Update it to pass `a`:
```go
case *events.Message:
	incoming, ok := translateIncoming(a, evt)
	if !ok {
		return
	}
	a.mu.Lock()
	h := a.incomingHandler
	a.mu.Unlock()
	if h != nil {
		h(incoming)
	}
```

- [ ] **Step 3: Build and run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: clean. The existing IncomingMessage tests don't construct values with MediaDownloader, which is fine — the field defaults to nil.

- [ ] **Step 4: Commit**

```bash
git add internal/waclient/whatsmeow_adapter.go
git commit -m "waclient: populate MediaDownloader closures for media kinds"
```

---

## Task 6: service.New takes mediastore + serve.go wiring

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`
- Modify: `cmd/whatsmeow-api/serve.go`

Signature change only — no new behavior. Tasks 7–9 add SendMedia / GetMediaRef / handleIncoming download.

- [ ] **Step 1: Update service.New signature**

Edit `internal/service/service.go`. Find:
```go
type svc struct {
	wa     waclient.WAClient
	bundle store.Bundle
	logger *slog.Logger
}

func New(wa waclient.WAClient, bundle store.Bundle, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &svc{wa: wa, bundle: bundle, logger: logger}
	wa.OnIncomingMessage(s.handleIncoming)
	return s
}
```

Replace with:
```go
type svc struct {
	wa         waclient.WAClient
	bundle     store.Bundle
	mediaStore *mediastore.Store
	logger     *slog.Logger
}

func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, logger *slog.Logger) Service {
	if logger == nil {
		logger = slog.Default()
	}
	s := &svc{wa: wa, bundle: bundle, mediaStore: mediaStore, logger: logger}
	wa.OnIncomingMessage(s.handleIncoming)
	return s
}
```

Add to the imports:
```go
"github.com/askarzh/whatsmeow-api/internal/mediastore"
```

- [ ] **Step 2: Update existing service tests**

Edit `internal/service/service_test.go`. Every `service.New(wa, bundle, nil)` call now needs a `mediastore.Store` arg. Use a per-test temp dir:

Add a helper near the top of the file (after the in-memory bundle helpers):
```go
func newTestMediaStore(t *testing.T) *mediastore.Store {
	t.Helper()
	return mediastore.New(t.TempDir())
}
```

Add to the imports:
```go
"github.com/askarzh/whatsmeow-api/internal/mediastore"
```

Then update every `service.New(wa, bundle, nil)` call site to:
```go
service.New(wa, bundle, newTestMediaStore(t), nil)
```

There are MANY of them — `grep -n "service.New(" internal/service/service_test.go`. Replace each one.

- [ ] **Step 3: Update serve.go**

Edit `cmd/whatsmeow-api/serve.go`. Find:
```go
svc := service.New(wa, appDB.Bundle(), logger)
```

Replace with:
```go
mediaDir := filepath.Join(cfg.DataDir, "media")
mediaStore := mediastore.New(mediaDir)
svc := service.New(wa, appDB.Bundle(), mediaStore, logger)
```

Add to the imports:
```go
"github.com/askarzh/whatsmeow-api/internal/mediastore"
```

`filepath` is already imported from Plan 02.

- [ ] **Step 4: Build, vet, run all tests**

```bash
go build ./...
go vet ./...
go test ./... -race
```

Expected: PASS. The signature change ripples cleanly; no behavior changes.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go cmd/whatsmeow-api/serve.go
git commit -m "service: New takes mediastore.Store"
```

---

## Task 7: service.SendMedia + GetMediaRef

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing tests**

Append to `internal/service/service_test.go`:
```go
func TestSendMediaSuccess(t *testing.T) {
	ctx := context.Background()
	bundle, chats, msgs, _ := newInMemoryBundle()
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	wa := &sendableFakeWA{
		sendResp: waclient.Sent{ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net"},
	}
	// Use a captured fakeWA wrapper that records SendMedia args.
	mediaWA := &mediaSenderFakeWA{sendableFakeWA: *wa, sendResp: waclient.Sent{
		ID: "MID1", Timestamp: now, SenderJID: "me@s.whatsapp.net",
	}}

	ms := newTestMediaStore(t)
	s := service.New(mediaWA, bundle, ms, nil)

	body := []byte("fake-image-bytes")
	got, err := s.SendMedia(ctx, service.SendMediaRequest{
		ChatJID: "27821234567@s.whatsapp.net",
		Kind:    "image",
		Caption: "hello",
		MIME:    "image/jpeg",
		Body:    body,
	})
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.ID)
	assert.Equal(t, "image", got.Kind)
	assert.Equal(t, "hello", got.Body) // caption stored in body

	// fake WA was called with right args
	assert.Equal(t, "27821234567@s.whatsapp.net", mediaWA.gotChatJID)
	assert.Equal(t, "image", mediaWA.gotKind)
	assert.Equal(t, body, mediaWA.gotBody)

	// message + chat persisted
	require.Contains(t, *msgs, "MID1")
	require.Contains(t, *chats, "27821234567@s.whatsapp.net")
}

func TestSendMediaValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)

	cases := []struct{ label string; req service.SendMediaRequest }{
		{"empty chat_jid", service.SendMediaRequest{Kind: "image", MIME: "image/jpeg", Body: []byte("x")}},
		{"empty body", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg"}},
		{"empty mime", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", Body: []byte("x")}},
		{"bad kind", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "video", MIME: "video/mp4", Body: []byte("x")}},
		{"document missing filename", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "document", MIME: "application/pdf", Body: []byte("x")}},
		{"caption too long", service.SendMediaRequest{ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg", Body: []byte("x"), Caption: strings.Repeat("c", 4097)}},
	}
	for _, tc := range cases {
		t.Run(tc.label, func(t *testing.T) {
			_, err := s.SendMedia(context.Background(), tc.req)
			require.Error(t, err)
			assert.True(t, errors.Is(err, service.ErrInvalidRequest))
		})
	}
}

func TestSendMediaNotConnected(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	wa := &mediaSenderFakeWA{sendErr: waclient.ErrNotConnected}
	s := service.New(wa, bundle, ms, nil)

	_, err := s.SendMedia(context.Background(), service.SendMediaRequest{
		ChatJID: "a@s.whatsapp.net", Kind: "image", MIME: "image/jpeg", Body: []byte("x"),
	})
	assert.True(t, errors.Is(err, waclient.ErrNotConnected))
}

func TestGetMediaRefHappyPath(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)

	require.NoError(t, bundle.Media.Put(context.Background(), store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: 100, SHA256: "abc", Path: "/tmp/abc.jpg",
	}))

	got, err := s.GetMediaRef(context.Background(), "MID1")
	require.NoError(t, err)
	assert.Equal(t, "MID1", got.MessageID)
	assert.Equal(t, "image/jpeg", got.MIME)
}

func TestGetMediaRefNotFound(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "missing")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}

func TestGetMediaRefValidation(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	s := service.New(&mediaSenderFakeWA{}, bundle, ms, nil)
	_, err := s.GetMediaRef(context.Background(), "")
	assert.True(t, errors.Is(err, service.ErrInvalidRequest))
}
```

Add the `mediaSenderFakeWA` helper near the existing `sendableFakeWA`:
```go
type mediaSenderFakeWA struct {
	sendableFakeWA

	sendResp   waclient.Sent
	sendErr    error
	gotChatJID string
	gotKind    string
	gotBody    []byte
}

func (f *mediaSenderFakeWA) SendMedia(_ context.Context, chatJID, kind, caption, filename, mime string, body []byte) (waclient.Sent, error) {
	f.gotChatJID = chatJID
	f.gotKind = kind
	f.gotBody = body
	return f.sendResp, f.sendErr
}
```

Add `"strings"` and `"errors"` to imports if not already.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/service/... -run 'TestSendMedia|TestGetMediaRef'
```

Expected: FAIL — service.SendMediaRequest, (*svc).SendMedia, (*svc).GetMediaRef all undefined.

- [ ] **Step 3: Implement on Service**

Edit `internal/service/service.go`. Add the request type near the existing `Stats`:
```go
type SendMediaRequest struct {
	ChatJID  string
	Kind     string // "image" | "document"
	Caption  string // optional, max 4096 bytes
	Filename string // required for document; informational for image
	MIME     string
	Body     []byte
}
```

Extend the `Service` interface:
```go
SendMedia(ctx context.Context, req SendMediaRequest) (store.Message, error)
GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error)
```

Append the methods at the bottom:
```go
func (s *svc) SendMedia(ctx context.Context, req SendMediaRequest) (store.Message, error) {
	if strings.TrimSpace(req.ChatJID) == "" {
		return store.Message{}, fmt.Errorf("%w: chat_jid is required", ErrInvalidRequest)
	}
	if len(req.Body) == 0 {
		return store.Message{}, fmt.Errorf("%w: body is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.MIME) == "" {
		return store.Message{}, fmt.Errorf("%w: mime is required", ErrInvalidRequest)
	}
	if req.Kind != "image" && req.Kind != "document" {
		return store.Message{}, fmt.Errorf("%w: kind must be image or document", ErrInvalidRequest)
	}
	if req.Kind == "document" && strings.TrimSpace(req.Filename) == "" {
		return store.Message{}, fmt.Errorf("%w: filename is required for documents", ErrInvalidRequest)
	}
	if len(req.Caption) > maxTextLen {
		return store.Message{}, fmt.Errorf("%w: caption exceeds %d bytes", ErrInvalidRequest, maxTextLen)
	}

	sent, err := s.wa.SendMedia(ctx, req.ChatJID, req.Kind, req.Caption, req.Filename, req.MIME, req.Body)
	if err != nil {
		return store.Message{}, err
	}

	sha, path, werr := s.mediaStore.Write(ctx, req.Body, req.MIME)
	if werr != nil {
		s.logger.Warn("write media to disk failed", "id", sent.ID, "err", werr)
		// Continue: message is out, local copy can be re-fetched via whatsmeow echo.
		sha = ""
		path = ""
	}

	msg := store.Message{
		ID:        sent.ID,
		ChatJID:   req.ChatJID,
		SenderJID: sent.SenderJID,
		Timestamp: sent.Timestamp,
		Kind:      req.Kind,
		Body:      req.Caption,
	}
	if err := s.bundle.Messages.Put(ctx, msg); err != nil {
		s.logger.Warn("persist outbound media message failed", "id", sent.ID, "err", err)
	}

	if path != "" {
		mediaRef := store.MediaRef{
			MessageID: sent.ID,
			MIME:      req.MIME,
			Size:      int64(len(req.Body)),
			SHA256:    sha,
			Path:      path,
		}
		if err := s.bundle.Media.Put(ctx, mediaRef); err != nil {
			s.logger.Warn("persist media row failed", "id", sent.ID, "err", err)
		}
	}

	// Upsert chat preserving unread_count (same pattern as SendText).
	existing, err := s.bundle.Chats.Get(ctx, req.ChatJID)
	if err != nil {
		existing = store.Chat{JID: req.ChatJID, Kind: waclient.ChatKindFromJID(req.ChatJID)}
	}
	existing.LastMsgAt = sent.Timestamp
	if existing.Kind == "" {
		existing.Kind = waclient.ChatKindFromJID(req.ChatJID)
	}
	if err := s.bundle.Chats.Put(ctx, existing); err != nil {
		s.logger.Warn("upsert chat on send media failed", "chat_jid", req.ChatJID, "err", err)
	}

	return msg, nil
}

func (s *svc) GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error) {
	if strings.TrimSpace(messageID) == "" {
		return store.MediaRef{}, fmt.Errorf("%w: message_id is required", ErrInvalidRequest)
	}
	return s.bundle.Media.GetByMessageID(ctx, messageID)
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v
```

Expected: PASS — Plan 02-05 tests + 6 new media tests.

- [ ] **Step 5: Bridge HTTP fakes**

Existing HTTP test fakes (`fakeStatusSvc`, `fakeLoginQRSvc`, `fakeLoginPhoneSvc`, `fakeLogoutSvc`, `fakeSendSvc`, `fakeChatsSvc`, `fakeContactsSvc`, `fakeStatsSvc`) all have `var _ service.Service = ...` checks that now require SendMedia + GetMediaRef stubs.

Add to each fake (across `status_test.go`, `login_qr_test.go`, `login_phone_test.go`, `logout_test.go`, `messages_test.go`, `chats_test.go`, `contacts_test.go`, `stats_test.go`):

```go
func (f fakeStatusSvc) SendMedia(context.Context, service.SendMediaRequest) (store.Message, error) {
	return store.Message{}, nil
}
func (f fakeStatusSvc) GetMediaRef(context.Context, string) (store.MediaRef, error) {
	return store.MediaRef{}, nil
}
```

(Adapt the receiver name + value-vs-pointer to match each fake's existing methods.)

- [ ] **Step 6: Run full suite**

```bash
go test ./... -race
```

Expected: PASS.

- [ ] **Step 7: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go internal/transport/http/{status,login_qr,login_phone,logout,messages,chats,contacts,stats}_test.go
git commit -m "service: SendMedia + GetMediaRef with validation"
```

---

## Task 8: service.handleIncoming download goroutine

**Files:**
- Modify: `internal/service/service.go`
- Modify: `internal/service/service_test.go`

- [ ] **Step 1: Add the failing test**

Append to `internal/service/service_test.go`:
```go
func TestHandleIncomingDownloadsMedia(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	wa := &mediaSenderFakeWA{}
	s := service.New(wa, bundle, ms, nil)
	require.NotNil(t, wa.incoming)

	body := []byte("inbound-media-bytes")
	mime := "image/png"
	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN1",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image",
		PushName:  "C",
		MediaDownloader: func(_ context.Context) ([]byte, string, error) {
			return body, mime, nil
		},
	})

	// Wait briefly for the goroutine to persist the media row.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := bundle.Media.GetByMessageID(context.Background(), "MIN1")
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	got, err := bundle.Media.GetByMessageID(context.Background(), "MIN1")
	require.NoError(t, err)
	assert.Equal(t, "image/png", got.MIME)
	assert.Equal(t, int64(len(body)), got.Size)
	assert.NotEmpty(t, got.Path)

	// File on disk matches.
	disk, err := os.ReadFile(got.Path)
	require.NoError(t, err)
	assert.Equal(t, body, disk)
}

func TestHandleIncomingDownloadFailureLogged(t *testing.T) {
	bundle, _, _, _ := newInMemoryBundle()
	ms := newTestMediaStore(t)
	wa := &mediaSenderFakeWA{}
	s := service.New(wa, bundle, ms, nil)
	require.NotNil(t, wa.incoming)

	called := make(chan struct{}, 1)
	wa.incoming(waclient.IncomingMessage{
		ID:        "MIN2",
		ChatJID:   "chat@s.whatsapp.net",
		ChatKind:  "user",
		SenderJID: "chat@s.whatsapp.net",
		Timestamp: time.Unix(1000, 0).UTC(),
		Kind:      "image",
		MediaDownloader: func(_ context.Context) ([]byte, string, error) {
			called <- struct{}{}
			return nil, "", errors.New("simulated download failure")
		},
	})

	// Wait for the closure to run.
	select {
	case <-called:
	case <-time.After(2 * time.Second):
		t.Fatal("downloader never called")
	}
	// Give the goroutine a moment to handle the error.
	time.Sleep(50 * time.Millisecond)

	// No media row should exist (download failed).
	_, err := bundle.Media.GetByMessageID(context.Background(), "MIN2")
	assert.True(t, errors.Is(err, store.ErrNotFound))
}
```

Add `"os"` to imports if not already present.

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/service/... -run TestHandleIncomingDownload
```

Expected: FAIL — handleIncoming doesn't trigger any download yet.

- [ ] **Step 3: Implement download goroutine**

Edit `internal/service/service.go`. Find the existing `handleIncoming` method. After the `bundle.Messages.Put` line at the end, add:
```go
	if msg.MediaDownloader != nil {
		go s.downloadAndPersistMedia(msg.ID, msg.MediaDownloader)
	}
```

Append the method:
```go
func (s *svc) downloadAndPersistMedia(messageID string, downloader func(context.Context) ([]byte, string, error)) {
	ctx := context.Background()
	body, mime, err := downloader(ctx)
	if err != nil {
		s.logger.Warn("download media failed", "id", messageID, "err", err)
		return
	}

	sha, path, err := s.mediaStore.Write(ctx, body, mime)
	if err != nil {
		s.logger.Warn("write media to disk failed", "id", messageID, "err", err)
		return
	}

	if err := s.bundle.Media.Put(ctx, store.MediaRef{
		MessageID: messageID,
		MIME:      mime,
		Size:      int64(len(body)),
		SHA256:    sha,
		Path:      path,
	}); err != nil {
		s.logger.Warn("persist incoming media row failed", "id", messageID, "err", err)
	}
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/service/... -v -run TestHandleIncomingDownload
```

Expected: both PASS.

```bash
go test ./... -race
```

Expected: full suite green.

- [ ] **Step 5: Commit**

```bash
git add internal/service/service.go internal/service/service_test.go
git commit -m "service: handleIncoming spawns download goroutine for media"
```

---

## Task 9: HTTP SendMediaHandler + GetMediaHandler

**Files:**
- Create: `internal/transport/http/media.go`
- Create: `internal/transport/http/media_test.go`
- Modify: `internal/transport/http/router.go`

- [ ] **Step 1: Write the failing tests**

Create `internal/transport/http/media_test.go`:
```go
package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	httpapi "github.com/askarzh/whatsmeow-api/internal/transport/http"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeMediaSvc struct {
	sendResp store.Message
	sendErr  error
	getResp  store.MediaRef
	getErr   error

	gotReq         service.SendMediaRequest
	gotMessageID   string
}

func (f *fakeMediaSvc) Status(context.Context) (waclient.Status, error) {
	return waclient.Status{}, nil
}
func (f *fakeMediaSvc) LoginQR(context.Context) (<-chan waclient.QREvent, error) {
	return nil, nil
}
func (f *fakeMediaSvc) LoginPhone(context.Context, string) (<-chan waclient.PairEvent, error) {
	return nil, nil
}
func (f *fakeMediaSvc) Logout(context.Context) error { return nil }
func (f *fakeMediaSvc) SendText(context.Context, string, string) (store.Message, error) {
	return store.Message{}, nil
}
func (f *fakeMediaSvc) ListChats(context.Context, time.Time, int, bool) ([]store.Chat, error) {
	return nil, nil
}
func (f *fakeMediaSvc) GetChat(context.Context, string) (store.Chat, error) {
	return store.Chat{}, nil
}
func (f *fakeMediaSvc) ListMessages(context.Context, string, time.Time, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeMediaSvc) SearchMessages(context.Context, string, int) ([]store.Message, error) {
	return nil, nil
}
func (f *fakeMediaSvc) ListContacts(context.Context) ([]store.Contact, error) { return nil, nil }
func (f *fakeMediaSvc) SearchContacts(context.Context, string, int) ([]store.Contact, error) {
	return nil, nil
}
func (f *fakeMediaSvc) Stats(context.Context) (service.Stats, error) {
	return service.Stats{}, nil
}
func (f *fakeMediaSvc) SendMedia(_ context.Context, req service.SendMediaRequest) (store.Message, error) {
	f.gotReq = req
	return f.sendResp, f.sendErr
}
func (f *fakeMediaSvc) GetMediaRef(_ context.Context, id string) (store.MediaRef, error) {
	f.gotMessageID = id
	return f.getResp, f.getErr
}

var _ service.Service = (*fakeMediaSvc)(nil)

// makeMultipart builds a multipart/form-data body with the given fields.
func makeMultipart(t *testing.T, fields map[string]string, file *struct{ Field, Filename, ContentType string; Body []byte }) (*bytes.Buffer, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	w := multipart.NewWriter(buf)
	for k, v := range fields {
		require.NoError(t, w.WriteField(k, v))
	}
	if file != nil {
		hdr := make(map[string][]string)
		hdr["Content-Disposition"] = []string{`form-data; name="` + file.Field + `"; filename="` + file.Filename + `"`}
		hdr["Content-Type"] = []string{file.ContentType}
		fw, err := w.CreatePart(hdr)
		require.NoError(t, err)
		_, err = fw.Write(file.Body)
		require.NoError(t, err)
	}
	require.NoError(t, w.Close())
	return buf, w.FormDataContentType()
}

func TestSendMediaHappyPath(t *testing.T) {
	now := time.Date(2026, 5, 2, 12, 0, 0, 0, time.UTC)
	f := &fakeMediaSvc{sendResp: store.Message{
		ID: "MID1", ChatJID: "27821234567@s.whatsapp.net", Timestamp: now, Kind: "image", Body: "hi",
	}}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	imageBytes := []byte("fake-image-bytes")
	body, ct := makeMultipart(t,
		map[string]string{
			"chat_jid": "27821234567@s.whatsapp.net",
			"kind":     "image",
			"caption":  "hi",
		},
		&struct{ Field, Filename, ContentType string; Body []byte }{
			Field: "file", Filename: "img.jpg", ContentType: "image/jpeg", Body: imageBytes,
		},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusCreated, res.StatusCode)

	assert.Equal(t, "27821234567@s.whatsapp.net", f.gotReq.ChatJID)
	assert.Equal(t, "image", f.gotReq.Kind)
	assert.Equal(t, "hi", f.gotReq.Caption)
	assert.Equal(t, "image/jpeg", f.gotReq.MIME)
	assert.Equal(t, imageBytes, f.gotReq.Body)

	var resp struct {
		ID      string `json:"id"`
		ChatJID string `json:"chat_jid"`
		Ts      string `json:"ts"`
	}
	require.NoError(t, json.NewDecoder(res.Body).Decode(&resp))
	assert.Equal(t, "MID1", resp.ID)
}

func TestSendMediaMissingFile(t *testing.T) {
	f := &fakeMediaSvc{}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		nil,
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaBadKind(t *testing.T) {
	f := &fakeMediaSvc{sendErr: service.ErrInvalidRequest}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "video"},
		&struct{ Field, Filename, ContentType string; Body []byte }{
			Field: "file", Filename: "v.mp4", ContentType: "video/mp4", Body: []byte("x"),
		},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaTooLarge(t *testing.T) {
	f := &fakeMediaSvc{}
	// Cap at 32 bytes; send 1 KB.
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 32))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		&struct{ Field, Filename, ContentType string; Body []byte }{
			Field: "file", Filename: "big.bin", ContentType: "image/jpeg", Body: bytes.Repeat([]byte{0x42}, 1024),
		},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusBadRequest, res.StatusCode)
}

func TestSendMediaNotConnected(t *testing.T) {
	f := &fakeMediaSvc{sendErr: waclient.ErrNotConnected}
	srv := httptest.NewServer(httpapi.SendMediaHandler(f, 100*1024*1024))
	defer srv.Close()

	body, ct := makeMultipart(t,
		map[string]string{"chat_jid": "a@s.whatsapp.net", "kind": "image"},
		&struct{ Field, Filename, ContentType string; Body []byte }{
			Field: "file", Filename: "img.jpg", ContentType: "image/jpeg", Body: []byte("x"),
		},
	)
	res, err := http.Post(srv.URL, ct, body)
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusConflict, res.StatusCode)
}

func TestGetMediaHappyPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "abc.jpg")
	body := []byte("file-bytes")
	require.NoError(t, os.WriteFile(path, body, 0o600))

	f := &fakeMediaSvc{getResp: store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: int64(len(body)), SHA256: "abc", Path: path,
	}}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/MID1")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusOK, res.StatusCode)
	assert.Equal(t, "image/jpeg", res.Header.Get("Content-Type"))
	got, err := io.ReadAll(res.Body)
	require.NoError(t, err)
	assert.Equal(t, body, got)
	assert.Equal(t, "MID1", f.gotMessageID)
}

func TestGetMediaNotFound(t *testing.T) {
	f := &fakeMediaSvc{getErr: store.ErrNotFound}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/missing")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusNotFound, res.StatusCode)
}

func TestGetMediaFileMissing(t *testing.T) {
	f := &fakeMediaSvc{getResp: store.MediaRef{
		MessageID: "MID1", MIME: "image/jpeg", Size: 100, SHA256: "abc",
		Path: "/tmp/definitely-does-not-exist-87213.bin",
	}}
	r := chi.NewRouter()
	r.Get("/v1/media/{message_id}", httpapi.GetMediaHandler(f).ServeHTTP)
	srv := httptest.NewServer(r)
	defer srv.Close()

	res, err := http.Get(srv.URL + "/v1/media/MID1")
	require.NoError(t, err)
	defer res.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, res.StatusCode)
}

// errors import for the fake
var _ = errors.New
```

- [ ] **Step 2: Confirm failure**

```bash
go test ./internal/transport/http/... -run 'TestSendMedia|TestGetMedia'
```

Expected: FAIL — handlers undefined.

- [ ] **Step 3: Implement the handlers**

Create `internal/transport/http/media.go`:
```go
package http

import (
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/askarzh/whatsmeow-api/internal/service"
	"github.com/askarzh/whatsmeow-api/internal/store"
	"github.com/askarzh/whatsmeow-api/internal/waclient"
	"github.com/go-chi/chi/v5"
)

// SendMediaHandler handles POST /v1/media (multipart/form-data).
//
// Form fields:
//   chat_jid (text, required)
//   kind     (text, required: "image" | "document")
//   caption  (text, optional, ≤ 4096 bytes)
//   filename (text, required if kind=document)
//   file     (file, required, body capped at maxBodyBytes)
func SendMediaHandler(svc service.Service, maxBodyBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Cap the request body before parsing.
		r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)

		if err := r.ParseMultipartForm(maxBodyBytes); err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "multipart parse: "+err.Error())
			return
		}

		chatJID := r.FormValue("chat_jid")
		kind := r.FormValue("kind")
		caption := r.FormValue("caption")
		filename := r.FormValue("filename")

		filePart, fileHeader, err := r.FormFile("file")
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "file is required")
			return
		}
		defer filePart.Close()

		body, err := io.ReadAll(filePart)
		if err != nil {
			WriteProblem(w, http.StatusBadRequest, "request.invalid", "read file: "+err.Error())
			return
		}

		mimeType := pickMIME(fileHeader, filename)

		req := service.SendMediaRequest{
			ChatJID:  chatJID,
			Kind:     kind,
			Caption:  caption,
			Filename: filename,
			MIME:     mimeType,
			Body:     body,
		}
		msg, err := svc.SendMedia(r.Context(), req)
		if err != nil {
			switch {
			case errors.Is(err, service.ErrInvalidRequest):
				WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			case errors.Is(err, waclient.ErrNotConnected):
				WriteProblem(w, http.StatusConflict, "wa.not_connected", err.Error())
			default:
				WriteProblem(w, http.StatusInternalServerError, "wa.send_failed", err.Error())
			}
			return
		}

		writeJSON(w, http.StatusCreated, map[string]any{
			"id":       msg.ID,
			"chat_jid": msg.ChatJID,
			"ts":       msg.Timestamp.UTC().Format(time.RFC3339),
		})
	})
}

// pickMIME determines the media type from (1) the multipart part's
// Content-Type header, (2) the filename's extension, or (3) returns
// "application/octet-stream" as a fallback. Plan 06 trusts the uploader.
func pickMIME(fh *multipart.FileHeader, filename string) string {
	if fh != nil && fh.Header != nil {
		if ct := fh.Header.Get("Content-Type"); ct != "" && ct != "application/octet-stream" {
			return ct
		}
	}
	if ext := filepath.Ext(filename); ext != "" {
		if t := mime.TypeByExtension(ext); t != "" {
			return t
		}
	}
	return "application/octet-stream"
}

// GetMediaHandler handles GET /v1/media/{message_id}: streams stored bytes.
func GetMediaHandler(svc service.Service) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		messageID := chi.URLParam(r, "message_id")
		ref, err := svc.GetMediaRef(r.Context(), messageID)
		switch {
		case err == nil:
			// fall through
		case errors.Is(err, store.ErrNotFound):
			WriteProblem(w, http.StatusNotFound, "media.not_found", "no media for that message id")
			return
		case errors.Is(err, service.ErrInvalidRequest):
			WriteProblem(w, http.StatusBadRequest, "request.invalid", err.Error())
			return
		default:
			WriteProblem(w, http.StatusInternalServerError, "internal", err.Error())
			return
		}

		f, err := os.Open(ref.Path)
		if err != nil {
			WriteProblem(w, http.StatusInternalServerError, "media.read_failed", err.Error())
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", ref.MIME)
		w.Header().Set("Content-Length", strconv.FormatInt(ref.Size, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
	})
}
```

- [ ] **Step 4: Wire the routes**

Edit `internal/transport/http/router.go`. Append in the auth-protected group:
```go
r.Method(http.MethodPost, "/media", SendMediaHandler(d.Service, d.Config.HTTP.MaxBodyBytes))
r.Method(http.MethodGet, "/media/{message_id}", GetMediaHandler(d.Service))
```

- [ ] **Step 5: Run tests**

```bash
go test ./internal/transport/http/... -v
go test ./... -race
```

Expected: PASS — existing tests + 8 new media tests.

- [ ] **Step 6: Commit**

```bash
git add internal/transport/http/media.go internal/transport/http/media_test.go internal/transport/http/router.go
git commit -m "http: POST /v1/media + GET /v1/media/{message_id}"
```

---

## Task 10: End-to-end smoke test

**Files:** none modified.

This task verifies the validation paths against an empty DB, plus optional real WhatsApp round-trip if you have a paired account.

- [ ] **Step 1: Build and start the daemon**

```bash
pkill -f "whatsmeow-api serve" 2>/dev/null; sleep 1
make build
rm -rf data
./bin/whatsmeow-api serve > /tmp/wmapi.log 2>&1 &
sleep 2
cat /tmp/wmapi.log
```

Expected: `app store opened`, `server starting`, no errors.

- [ ] **Step 2: Verify validation failures**

```bash
# No file part → 400
curl -i -X POST -F "chat_jid=27821234567@s.whatsapp.net" -F "kind=image" \
  http://127.0.0.1:8080/v1/media

# Bad kind → 400
echo "fake bytes" > /tmp/test.jpg
curl -i -X POST \
  -F "chat_jid=27821234567@s.whatsapp.net" \
  -F "kind=video" \
  -F "file=@/tmp/test.jpg;type=image/jpeg" \
  http://127.0.0.1:8080/v1/media

# Document missing filename → 400
curl -i -X POST \
  -F "chat_jid=27821234567@s.whatsapp.net" \
  -F "kind=document" \
  -F "file=@/tmp/test.jpg;type=application/pdf" \
  http://127.0.0.1:8080/v1/media

# Valid request but daemon not connected → 409
curl -i -X POST \
  -F "chat_jid=27821234567@s.whatsapp.net" \
  -F "kind=image" \
  -F "file=@/tmp/test.jpg;type=image/jpeg" \
  http://127.0.0.1:8080/v1/media
```

Expected: 400 / 400 / 400 / 409 with appropriate `code` fields.

- [ ] **Step 3: Verify GET 404**

```bash
curl -i http://127.0.0.1:8080/v1/media/missing
```

Expected: 404, `code: media.not_found`.

- [ ] **Step 4: (Optional) Real round-trip with a paired account**

Re-pair if needed: `./bin/whatsmeow-api login qr`. Then:
```bash
JID="<YOUR_OWN_JID>"  # e.g. 27821234567@s.whatsapp.net
curl -i -X POST \
  -F "chat_jid=$JID" \
  -F "kind=image" \
  -F "caption=Plan 06 smoke test" \
  -F "file=@/path/to/real-image.jpg;type=image/jpeg" \
  http://127.0.0.1:8080/v1/media
```

Expected: 201 with `{"id":"3EB...","chat_jid":"...","ts":"..."}`. The image arrives on the recipient phone.

```bash
# DB row check
sqlite3 data/whatsmeow-app.db 'SELECT message_id, mime, size, length(sha256), path FROM media'
```

Expected: a row for the just-sent message; `path` exists on disk.

```bash
# GET it back
curl -s -o /tmp/roundtrip.jpg "http://127.0.0.1:8080/v1/media/<id-from-201>"
diff /path/to/real-image.jpg /tmp/roundtrip.jpg
```

Expected: identical bytes.

Send an image FROM your phone TO the linked account, wait ~5 seconds, then:
```bash
sqlite3 data/whatsmeow-app.db 'SELECT id, chat_jid, kind FROM messages ORDER BY ts DESC LIMIT 3'
sqlite3 data/whatsmeow-app.db 'SELECT message_id, mime, size FROM media ORDER BY message_id DESC LIMIT 3'
```

Expected: a new `messages` row with `kind=image`, and a `media` row with the bytes. The download was async — give it a moment.

- [ ] **Step 5: Stop the daemon**

```bash
kill -TERM $(pgrep -f "whatsmeow-api serve")
sleep 1
tail -5 /tmp/wmapi.log
```

Expected last log line: `... msg="server stopped"`.

- [ ] **Step 6: No commit**

This task verifies behavior; nothing to commit.

---

## Task 11: Update README

**Files:**
- Modify: `README.md`

- [ ] **Step 1: Update the Status section**

Edit `README.md`. Replace:
```markdown
- **Plan 05 (list + search)** shipped: read-side endpoints over the app store. `GET /v1/chats` (cursor pagination), `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages` (cursor pagination), `GET /v1/messages/search?q=`, `GET /v1/contacts`, `GET /v1/contacts/search?q=`, `GET /v1/stats`.

Reactions / replies / edits / deletes / read receipts land in Plan 07; media in Plan 06; SSE event stream in Plan 09.
```

…with:
```markdown
- **Plan 05 (list + search)** shipped: read-side endpoints over the app store. `GET /v1/chats` (cursor pagination), `GET /v1/chats/{jid}`, `GET /v1/chats/{jid}/messages` (cursor pagination), `GET /v1/messages/search?q=`, `GET /v1/contacts`, `GET /v1/contacts/search?q=`, `GET /v1/stats`.
- **Plan 06 (media)** shipped: `POST /v1/media` (multipart/form-data) sends image + document outbound; `GET /v1/media/{message_id}` streams stored bytes; inbound media events auto-download in a background goroutine for all 5 kinds (image, video, audio, document, sticker). Files live under `data_dir/media/<sha[0:2]>/<sha>.<ext>` (content-addressable). Body cap configurable via `[http] max_body_bytes` (default 100 MiB).

Reactions / replies / edits / deletes / read receipts land in Plan 07; SSE event stream in Plan 09. Video/audio/sticker outbound deferred to a sibling plan.
```

- [ ] **Step 2: Commit**

```bash
git add README.md
git commit -m "docs: README update for Plan 06"
```

---

## Done — verification

- [ ] `go build ./...` clean
- [ ] `go vet ./...` clean
- [ ] `go test ./... -race` PASS, including new mediastore + service + HTTP tests
- [ ] Manual smoke (Task 10 Steps 1-3) all green: validation paths return correct 4xx; GET unknown returns 404
- [ ] (Optional with paired account) Task 10 Step 4: outbound image + document round-trip; inbound media auto-downloads to disk
- [ ] `git log --oneline` shows ~10 well-scoped commits

When all the above are checked, this plan is complete and the codebase is ready for **Plan 07 — reactions / replies / edits / deletes / read receipts / typing**.
