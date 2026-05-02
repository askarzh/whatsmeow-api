# whatsmeow-api Plan 06 — Media (Send + Receive + Download) Design

**Date:** 2026-05-02
**Status:** Approved (pending written-spec review)
**Repo:** `github.com/askarzh/whatsmeow-api`
**Predecessor:** Plan 05 (list + search) — merged.

## 1. Purpose

Add media handling to the daemon. Inbound media events from whatsmeow are auto-downloaded and persisted to disk. Clients can upload + send images and documents via `POST /v1/media`, and download stored media for any message via `GET /v1/media/{message_id}`. Plan 04's persistence already records the message row with `kind="image"`/`"video"`/etc. and empty body; Plan 06 fills in the bytes.

## 2. Goals

- Inbound: all five media kinds whatsmeow emits (image, video, audio, document, sticker) auto-download. Persistence is keyed by SHA-256 (content-addressable), so duplicates dedupe naturally.
- Outbound: `POST /v1/media` accepts multipart/form-data with image or document only. Video/audio/sticker outbound are deferred to a sibling plan because each has type-specific proto fields (duration, PTT flag, etc.) and adds surface area without much marginal benefit.
- Download: `GET /v1/media/{message_id}` streams the stored file with the correct `Content-Type`. 404 if no media row exists yet (the inbound download is async and may not have finished).
- Disk layout: `data_dir/media/<sha256[0:2]>/<sha256>.<ext>`. ~256 top-level dirs at saturation; comfortable for any plausible volume.
- Inbound download runs in a per-message goroutine — the message row lands synchronously in `handleIncoming` (so chat lists update instantly), the bytes follow asynchronously.

## 3. Non-goals (Plan 06)

- Video / audio / sticker outbound. Sibling plan; each kind needs type-specific proto construction.
- Streaming uploads. Plan 06 buffers the whole multipart body in memory under a configurable cap (default 100 MB).
- Resumable / chunked uploads on either direction.
- Compression, transcoding, or format conversion.
- Thumbnail extraction or generation. whatsmeow's events.Message often carries a thumbnail — we ignore it.
- Media-file garbage collection. Plan 03's soft-delete leaves the message row with `deleted_at` set; the file stays on disk. Cleanup is a future plan once retention policy lands.
- Inline media in `GET /v1/chats/{jid}/messages` responses. The kind is exposed; clients fetch bytes via `GET /v1/media/{id}` separately. Plan 06 keeps the list endpoints stable.

## 4. Architecture

```
INBOUND (auto-download)
  whatsmeow events.Message (media kind)
        │
        ▼
  adapter.onEvent → translateIncoming →
  IncomingMessage{ID, ChatJID, ..., MediaDownloader: closure}
        │
        ▼
  service.handleIncoming
        ├── persist Contacts.Put / Chats.Put / Messages.Put  (sync — fast)
        └── if MediaDownloader != nil:
              go s.downloadAndPersistMedia(messageID, downloader)
                    └── closure() → bytes + mime
                          → mediastore.Write(bytes, mime) → sha + path
                          → bundle.Media.Put(MediaRef{...})
                    Errors logged via slog; never returned.

OUTBOUND
  POST /v1/media (multipart/form-data)
        │
        ▼
  SendMediaHandler
        ├── parse multipart with cap (max_body_bytes)
        ├── validate (chat_jid non-empty, kind in {image,document}, body non-empty,
        │             mime non-empty, filename required for document, caption ≤ 4096)
        └── call service.SendMedia(req)
              ├── waclient.SendMedia(...)
              │     └── client.Upload(ctx, body, mediaType)
              │     └── build *waE2E.Message{ImageMessage / DocumentMessage}
              │     └── client.SendMessage(ctx, jid, msg)
              ├── mediastore.Write(body, mime) → sha + path
              ├── bundle.Messages.Put + bundle.Chats.Put + bundle.Media.Put
              └── return store.Message
        201 {id, chat_jid, ts}

DOWNLOAD
  GET /v1/media/{message_id}
        │
        ▼
  GetMediaHandler
        ├── service.GetMediaRef(messageID) → store.MediaRef
        ├── ErrNotFound → 404 media.not_found
        └── open file → set Content-Type from MIME → io.Copy to response
```

## 5. WAClient interface changes

```go
// internal/waclient/waclient.go

// IncomingMessage gains MediaDownloader. nil for non-media kinds and for
// kinds the adapter cannot download (none in current scope, but the field
// is optional so we don't break callers if a future kind lacks bytes).
type IncomingMessage struct {
    ID        string
    ChatJID   string
    ChatKind  string
    SenderJID string
    Timestamp time.Time
    Kind      string
    Body      string
    PushName  string

    // NEW in Plan 06.
    MediaDownloader func(ctx context.Context) ([]byte, string /* mime */, error)
}

type WAClient interface {
    // existing surface (Status, Resume, LoginQR, LoginPhone, Logout, Close,
    // SendText, OnIncomingMessage)

    // Plan 06
    SendMedia(ctx context.Context, chatJID, kind, caption, filename, mime string, body []byte) (Sent, error)
}
```

`kind` accepts only `"image"` and `"document"` in Plan 06; the adapter rejects others with an error.

## 6. waclient adapter implementation

`SendMedia`:
1. Lock, check `client != nil && IsConnected && IsLoggedIn` → else return `ErrNotConnected`.
2. Snapshot `senderJID` and `client`, release lock.
3. `types.ParseJID(chatJID)`.
4. Map kind → `whatsmeow.MediaType`: `"image"` → `whatsmeow.MediaImage`, `"document"` → `whatsmeow.MediaDocument`. Other kinds return `fmt.Errorf("unsupported media kind: %s", kind)`.
5. `client.Upload(ctx, body, mediaType)` → returns `whatsmeow.UploadResponse` with URL, MediaKey, FileEncSHA256, FileSHA256, FileLength, etc.
6. Build `*waE2E.Message` per kind (helper `buildMediaProto`):
   - `"image"`: `&waE2E.Message{ImageMessage: &waE2E.ImageMessage{Url, MediaKey, Mimetype, Caption, FileSHA256, FileEncSHA256, FileLength, DirectPath}}`
   - `"document"`: `&waE2E.Message{DocumentMessage: &waE2E.DocumentMessage{Url, MediaKey, Mimetype, Title (= filename), FileName (= filename), Caption, FileSHA256, FileEncSHA256, FileLength, DirectPath}}`
   All `*string`/`*uint64` fields get `proto.String(...)` / `proto.Uint64(...)`.
7. `client.SendMessage(ctx, parsedJID, msg)` → return `Sent{ID: resp.ID, Timestamp: resp.Timestamp, SenderJID: senderJID}`.

`translateIncoming` (extending Plan 04's helper): when `messageKindAndBody` returns a media kind (image/video/audio/document/sticker), populate `MediaDownloader` with a closure that captures the relevant submessage:

```go
case m.ImageMessage != nil:
    return "image", "", true, func(ctx context.Context) ([]byte, string, error) {
        body, err := a.client.Download(ctx, m.ImageMessage)
        if err != nil { return nil, "", err }
        return body, m.ImageMessage.GetMimetype(), nil
    }
```

`a` (the adapter) is captured in the closure so the goroutine can still talk to whatsmeow even after the event handler returns. The adapter's lifetime equals the daemon's, so this is safe.

Repeat for VideoMessage, AudioMessage, DocumentMessage, StickerMessage. Each is ~5 lines.

## 7. mediastore package

New small package `internal/mediastore`. Sole owner of on-disk media files.

```go
package mediastore

type Store struct{ root string }

// New constructs a Store rooted at root. The directory is created lazily
// on first Write.
func New(root string) *Store

// Write hashes body, derives the on-disk path from sha + ExtFromMIME(mime),
// ensures the parent dir exists, and atomically writes the file (write to
// a .tmp sibling, then rename). If the destination file already exists at
// the right size, no rewrite (idempotent on re-upload of identical bytes).
// Returns (sha256Hex, fullPath, error).
func (s *Store) Write(ctx context.Context, body []byte, mime string) (sha256Hex, path string, err error)

// Path returns the on-disk path for a given sha + extension (no IO).
func (s *Store) Path(sha256Hex, ext string) string

// ExtFromMIME maps common MIME types to file extensions: "image/jpeg"→".jpg",
// "image/png"→".png", "image/webp"→".webp", "image/gif"→".gif",
// "video/mp4"→".mp4", "audio/ogg"→".ogg", "audio/mpeg"→".mp3",
// "application/pdf"→".pdf", "application/zip"→".zip", and so on.
// Falls back to ".bin" for unknown types.
func ExtFromMIME(mime string) string
```

Disk layout: `<root>/<sha[0:2]>/<sha>.<ext>`. Atomic write pattern: open `<path>.tmp`, write all bytes, fsync, close, rename to `<path>`.

## 8. Service layer

```go
// internal/service/service.go

type SendMediaRequest struct {
    ChatJID  string
    Kind     string // "image" | "document"
    Caption  string // optional (≤ 4096 bytes)
    Filename string // required for document; informational for image
    MIME     string
    Body     []byte
}

type Service interface {
    // Plan 02-05 surface (Status, Login*, Logout, SendText, ListChats,
    // GetChat, ListMessages, SearchMessages, ListContacts, SearchContacts, Stats)

    // Plan 06
    SendMedia(ctx context.Context, req SendMediaRequest) (store.Message, error)
    GetMediaRef(ctx context.Context, messageID string) (store.MediaRef, error)
}

func New(wa waclient.WAClient, bundle store.Bundle, mediaStore *mediastore.Store, logger *slog.Logger) Service
```

`SendMedia`:
1. Validate: `ChatJID != ""`, `Body` non-empty, `MIME != ""`, `Kind in {image, document}`, `Filename != ""` if Kind=document, `len(Caption) <= 4096`. Failures → `ErrInvalidRequest`.
2. Call `wa.SendMedia(ctx, req.ChatJID, req.Kind, req.Caption, req.Filename, req.MIME, req.Body)`. Bubble up `waclient.ErrNotConnected`.
3. Write to disk: `sha, path, err := s.mediaStore.Write(ctx, req.Body, req.MIME)`. Failure logged but not propagated; the message has been sent — losing the local copy is recoverable from whatsmeow's own echo.
4. Persist message: `bundle.Messages.Put({ID: sent.ID, ChatJID, SenderJID: sent.SenderJID, Timestamp: sent.Timestamp, Kind: req.Kind, Body: req.Caption})`.
5. Persist media row: `bundle.Media.Put({MessageID: sent.ID, MIME: req.MIME, Size: int64(len(req.Body)), SHA256: sha, Path: path})`. Persist failures logged.
6. Upsert chat (read-modify-write to preserve `unread_count`, same pattern as SendText).
7. Return constructed `store.Message`.

`GetMediaRef`:
- Empty validation: `messageID == ""` → `ErrInvalidRequest`.
- Delegate to `bundle.Media.GetByMessageID(ctx, messageID)`. `store.ErrNotFound` flows through.

`handleIncoming` extension (after the existing message-persist):
```go
if msg.MediaDownloader != nil {
    go s.downloadAndPersistMedia(msg.ID, msg.MediaDownloader)
}
```

`downloadAndPersistMedia(messageID string, downloader func(ctx) ([]byte, string, error))`:
- Use `context.Background()` (event-context cancellation must not abort the download).
- Call `downloader(ctx)` → bytes + mime. Errors logged + return.
- `s.mediaStore.Write(ctx, bytes, mime)` → sha + path.
- `bundle.Media.Put({MessageID: messageID, MIME: mime, Size: int64(len(bytes)), SHA256: sha, Path: path})`. Errors logged.

## 9. HTTP handlers

`internal/transport/http/media.go` (new):

```go
// SendMediaHandler handles POST /v1/media.
//
// multipart/form-data fields:
//   chat_jid (text, required)
//   kind     (text, required: "image" | "document")
//   caption  (text, optional, max 4096 bytes)
//   filename (text, required if kind=document)
//   file     (file, required, body capped at maxBodyBytes)
//
// 201: {"id":"...","chat_jid":"...","ts":"..."}
func SendMediaHandler(svc service.Service, maxBodyBytes int64) http.Handler

// GetMediaHandler handles GET /v1/media/{message_id}.
// Streams the stored bytes with Content-Type from the stored mime.
// 404 media.not_found if no media row (download still in flight, or never had media).
func GetMediaHandler(svc service.Service) http.Handler
```

`SendMediaHandler` parses with `r.ParseMultipartForm(maxBodyBytes)`. The `file` part is read into a `[]byte`. After validation it constructs a `service.SendMediaRequest` and delegates. Status mapping mirrors `SendTextHandler` from Plan 04 plus 400 cases for "missing file" / "missing filename for document" / "kind unsupported".

`GetMediaHandler`:
1. `messageID := chi.URLParam(r, "message_id")`.
2. `ref, err := svc.GetMediaRef(ctx, messageID)`.
3. `errors.Is(err, store.ErrNotFound)` → `404 media.not_found`.
4. Open `ref.Path`, set `Content-Type: ref.MIME`, set `Content-Length: ref.Size`, `io.Copy(w, file)`. Errors during Open → 500.

Routes wired in `router.go`:
```go
r.Method(http.MethodPost, "/media", SendMediaHandler(d.Service, d.Config.HTTP.MaxBodyBytes))
r.Method(http.MethodGet, "/media/{message_id}", GetMediaHandler(d.Service))
```

## 10. Config

`internal/config/config.go` gains an `HTTPConfig` struct (or extends an existing one) with:
```go
type HTTPConfig struct {
    MaxBodyBytes int64 `koanf:"max_body_bytes"` // default 100 * 1024 * 1024
}
```

`config.example.toml` documents `[http]` with `max_body_bytes = 104857600`. Default applied in `config.Load`.

## 11. Wiring

`cmd/whatsmeow-api/serve.go`:
```go
mediaDir := filepath.Join(cfg.DataDir, "media")
mediaStore := mediastore.New(mediaDir)
svc := service.New(wa, appDB.Bundle(), mediaStore, logger)
```

The `data_dir/media/` directory is created lazily on first `Write` — no MkdirAll up front.

`internal/transport/http/router.go`'s `Deps` already carries `Config`; the handler reads `d.Config.HTTP.MaxBodyBytes` at registration time.

## 12. Testing strategy

**`internal/mediastore`** (`mediastore_test.go`):
- `TestWrite` — body + mime go in; sha is correct; file exists at expected path with expected content; idempotent on second call (file size unchanged, no rewrite needed).
- `TestExtFromMIME` — table-driven: known mimes map correctly, unknown returns `.bin`.
- `TestPath` — pure path computation, no IO.

**`internal/service`** (extend `service_test.go`):
- Extend `inMemoryBundle` with a `mediaStore` mock OR pass a real `*mediastore.Store{root: t.TempDir()}` (preferred — exercises real disk IO inside the test).
- `TestSendMediaSuccess` — fake WA returns `Sent`; verify Messages/Media/Chats rows persisted; bytes on disk match.
- `TestSendMediaValidation` — empty chat_jid, empty body, bad kind, missing filename for document, caption > 4096.
- `TestSendMediaNotConnected` — fake returns `ErrNotConnected`.
- `TestGetMediaRefHappyPath` / `TestGetMediaRefNotFound`.
- `TestHandleIncomingDownloadsMedia` — fake `IncomingMessage` with a `MediaDownloader` closure that returns canned bytes; verify the goroutine writes to disk and persists the media row. (Use a `sync` mechanism to wait — e.g., a channel signaled from the closure, or repeatedly poll `bundle.Media.GetByMessageID` for a small bounded duration.)
- `TestHandleIncomingDownloadFailureLogged` — closure returns an error; no media row appears; no panic.

**`internal/waclient`**: no automated tests for `SendMedia` — it requires real WhatsApp. Manual smoke covers it.

**`internal/transport/http`** (`media_test.go`):
- `TestSendMediaHappyPath` — multipart upload with a fake Service, capture the request, assert 201 + JSON shape.
- `TestSendMediaValidationMissingFile` — multipart without file field → 400.
- `TestSendMediaValidationBadKind` → 400.
- `TestSendMediaTooLarge` — body exceeds maxBodyBytes → 400.
- `TestSendMediaNotConnected` → fake returns `waclient.ErrNotConnected` → 409.
- `TestGetMediaHappyPath` — fake returns a MediaRef pointing at a temp file with known bytes; assert response body matches and Content-Type is correct.
- `TestGetMediaNotFound` → 404.
- `TestGetMediaFileMissing` — MediaRef points at a non-existent path → 500 (the disk file got deleted out from under us).

## 13. File layout

```
internal/mediastore/
  mediastore.go (new)              Store, Write, Path, ExtFromMIME
  mediastore_test.go (new)

internal/waclient/
  waclient.go                      IncomingMessage gains MediaDownloader; +SendMedia
  whatsmeow_adapter.go             SendMedia impl, populate MediaDownloader closures

internal/service/
  service.go                       +SendMediaRequest, +SendMedia, +GetMediaRef,
                                   handleIncoming download goroutine, New takes mediaStore
  service_test.go                  +tests; pass real mediastore.Store with t.TempDir()

internal/transport/http/
  media.go (new)                   SendMediaHandler, GetMediaHandler
  media_test.go (new)
  router.go                        +2 routes

internal/config/config.go          +HTTPConfig{MaxBodyBytes} (default 100 MB)
internal/config/config_test.go     extend defaults test
config.example.toml                +[http] max_body_bytes
cmd/whatsmeow-api/serve.go         construct mediastore + pass into service.New

README.md                          status section
```

## 14. Dependencies

None added. `mime/multipart` and `net/http` are stdlib. whatsmeow already provides `Client.Upload`, `Client.Download`, `MediaImage`/`MediaDocument` constants, and the `*waE2E.{Image,Document,Video,Audio,Sticker}Message` proto types.

## 15. Acceptance

- `go build ./...` clean.
- `go vet ./...` clean.
- `go test ./... -race` PASS, including new mediastore + service + HTTP tests.
- Manual smoke against a paired account:
  - Inbound: another phone sends an image to the daemon's account → daemon log shows download success → `data/media/<sha>.<ext>` exists → `GET /v1/media/<message_id>` returns the bytes with correct Content-Type.
  - Outbound: `curl -F chat_jid=... -F kind=image -F caption=... -F file=@/path/to/img.jpg .../v1/media` → 201 → image arrives on the recipient phone.
  - Outbound document: same but `-F kind=document -F filename=foo.pdf -F file=@foo.pdf`.
- Existing Plan 01–05 endpoints continue to pass.

## 16. Open questions deferred to implementation

- Whether to validate MIME against the file's magic bytes. Plan 06 trusts the uploader's MIME field. Future hardening can sniff via `http.DetectContentType` if abuse becomes a concern.
- Whether `POST /v1/media` should accept `mime` as an explicit form field or derive it from the file Content-Type header. Plan 06 derives it from the file part's `Content-Type` header (`mime/multipart` exposes it via `Part.Header`). Empty / `application/octet-stream` falls back to `mime.TypeByExtension(filepath.Ext(filename))`, then `application/octet-stream` if still unknown.
- Whether `Upload` requires the file to be on disk or accepts an `io.Reader`. The plan assumes whatsmeow accepts `[]byte`; the implementer verifies via `go doc go.mau.fi/whatsmeow.Client.Upload` and adapts (e.g. wrap in `bytes.NewReader`) if the signature differs.
- Inbound media filename: whatsmeow's `*waE2E.DocumentMessage.FileName` carries the original filename. We don't expose it on the `MediaRef` schema today (Plan 03 deferred it). For now, ignore on inbound; if a consumer needs it, add a `0002_*.sql` migration.
