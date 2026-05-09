# whatsmeow-api Cookbook

Copy-pasteable curl recipes for every HTTP endpoint. Assumes the daemon runs at `http://localhost:8080` with a bearer token in `$WMAPI_TOKEN`.

```bash
export WMAPI_BASE=http://localhost:8080
export WMAPI_TOKEN=your-bearer-token
```

All examples assume `jq` is installed for pretty-printing JSON.

## Health and status

### Liveness probe (no auth)

```bash
curl -sS "$WMAPI_BASE/v1/health"
# → {"status":"ok"}
```

### Daemon status

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" "$WMAPI_BASE/v1/status" | jq
# → {"jid":"15551234567@s.whatsapp.net","push_name":"...","since":"...","wa_connected":true}
```

## Login

### QR code (recommended)

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/login/qr"
# Streams SSE: `event: qr` frames with the encoded payload to display, then
# a terminal event on success.
```

### Phone-pair code

```bash
curl -N -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"phone_number":"+15551234567"}' \
     "$WMAPI_BASE/v1/login/phone"
# Streams SSE: `event: code` with the 8-character pairing code.
```

### Logout

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/logout"
# → 204 No Content
```

## Messages

### Send a text

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"chat_jid":"15557654321@s.whatsapp.net","text":"hello"}' \
     "$WMAPI_BASE/v1/messages" | jq
# → {"id":"3EB0...","chat_jid":"...","body":"hello",...}
```

### Reply to a message

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"chat_jid":"15557654321@s.whatsapp.net","text":"reply","reply_to":"3EB0..."}' \
     "$WMAPI_BASE/v1/messages" | jq
```

### Edit your message

```bash
curl -sS -X PATCH -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"text":"edited body"}' \
     "$WMAPI_BASE/v1/messages/3EB0..." | jq
```

### Delete (revoke) your message

```bash
curl -sS -X DELETE -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0..."
# → 204 No Content
```

## Media

### Send media (image, document, audio, video, sticker)

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -F "chat_jid=15557654321@s.whatsapp.net" \
     -F "kind=image" \
     -F "caption=look at this" \
     -F "file=@/path/to/photo.jpg" \
     "$WMAPI_BASE/v1/media" | jq
```

### Download persisted media bytes

```bash
curl -sS -OJ -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/media/3EB0..."
# Saves the file with the original filename via Content-Disposition.
```

## Reactions

### Add a reaction

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"emoji":"👍"}' \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions"
# → 204
```

### Clear your reaction

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"emoji":""}' \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions"
```

### List reactions on a message

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../reactions" | jq
```

## Receipts and typing

### Mark a received message as read

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../read"
# → 204
```

### Send "composing" / "paused" presence

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"state":"composing"}' \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net/typing"
# Pair with `{"state":"paused"}` when the user stops typing.
```

### List delivery / read receipts for a message

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/3EB0.../receipts" | jq
```

## Chats and search

### List chats (cursor pagination)

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats?limit=50" | jq
```

### Get one chat

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net" | jq
```

### List messages in a chat

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/chats/15557654321@s.whatsapp.net/messages?limit=50" | jq
```

### Search messages (full-text)

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/messages/search?q=hello&limit=20" | jq
```

### List contacts

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/contacts" | jq
```

### Search contacts

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/contacts/search?q=alice" | jq
```

### Stats

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/stats" | jq
```

## Groups

### Create a group

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"name":"Project X","participants":["15557654321@s.whatsapp.net"]}' \
     "$WMAPI_BASE/v1/groups" | jq
```

### List members

```bash
curl -sS -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/members" | jq
```

### Add or remove members

```bash
curl -sS -X POST -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Content-Type: application/json" \
     -d '{"action":"add","participants":["15559876543@s.whatsapp.net"]}' \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/members" | jq

# Remove with action:"remove"
```

### Leave a group

```bash
curl -sS -X DELETE -H "Authorization: Bearer $WMAPI_TOKEN" \
     "$WMAPI_BASE/v1/groups/120363xxxx@g.us/membership"
# → 204
```

## SSE event stream

### Subscribe (live tail from now)

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     "$WMAPI_BASE/v1/events"
```

You'll see a `:ready` comment, then a synthetic `connection.state` frame at `id: 0`, then real events as they happen.

### Resume from a known sequence

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     -H "Last-Event-ID: 4271" \
     "$WMAPI_BASE/v1/events"
# Replays everything with seq > 4271, then live-tails.
```

Or via query param:

```bash
curl -N -H "Authorization: Bearer $WMAPI_TOKEN" \
     -H "Accept: text/event-stream" \
     "$WMAPI_BASE/v1/events?since=4271"
```

### Event types (per Plan 09)

- `message.received` — inbound text/media
- `message.edited` — inbound EDIT
- `message.deleted` — inbound REVOKE
- `reaction.received` — inbound reaction (set or clear)
- `receipt.received` — delivered/read/played receipt
- `connection.state` — login/disconnect/reconnect transitions

Each event payload carries `"v": 1` for forward compatibility.
