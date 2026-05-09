CREATE TABLE chats (
    jid          TEXT PRIMARY KEY,
    name         TEXT,
    kind         TEXT NOT NULL,
    last_msg_at  TIMESTAMPTZ,
    unread_count INTEGER NOT NULL DEFAULT 0,
    archived     BOOLEAN NOT NULL DEFAULT FALSE
);

CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    chat_jid    TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE,
    sender_jid  TEXT NOT NULL,
    ts          TIMESTAMPTZ NOT NULL,
    kind        TEXT NOT NULL,
    body        TEXT,
    reply_to    TEXT,
    edited_at   TIMESTAMPTZ,
    deleted_at  TIMESTAMPTZ,
    raw_meta    TEXT,
    body_tsv    tsvector GENERATED ALWAYS AS (to_tsvector('simple', coalesce(body, ''))) STORED
);

CREATE INDEX idx_messages_chat_ts ON messages (chat_jid, ts DESC);
CREATE INDEX idx_messages_body_tsv ON messages USING GIN (body_tsv);

CREATE TABLE contacts (
    jid           TEXT PRIMARY KEY,
    push_name     TEXT,
    full_name     TEXT,
    business_name TEXT
);

CREATE TABLE media (
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       BIGINT NOT NULL,
    sha256     TEXT NOT NULL,
    path       TEXT NOT NULL
);

CREATE TABLE events_log (
    seq     BIGSERIAL PRIMARY KEY,
    ts      TIMESTAMPTZ NOT NULL,
    type    TEXT NOT NULL,
    payload TEXT NOT NULL
);

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
