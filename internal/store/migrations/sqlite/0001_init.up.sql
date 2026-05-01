CREATE TABLE chats (
    jid          TEXT PRIMARY KEY,
    name         TEXT,
    kind         TEXT NOT NULL,
    last_msg_at  INTEGER,
    unread_count INTEGER NOT NULL DEFAULT 0,
    archived     INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE messages (
    id          TEXT PRIMARY KEY,
    chat_jid    TEXT NOT NULL REFERENCES chats(jid) ON DELETE CASCADE,
    sender_jid  TEXT NOT NULL,
    ts          INTEGER NOT NULL,
    kind        TEXT NOT NULL,
    body        TEXT,
    reply_to    TEXT,
    edited_at   INTEGER,
    deleted_at  INTEGER,
    raw_meta    TEXT
);

CREATE INDEX idx_messages_chat_ts ON messages(chat_jid, ts DESC);

CREATE VIRTUAL TABLE messages_fts USING fts5(
    body,
    content='messages',
    content_rowid='rowid'
);

CREATE TRIGGER messages_ai AFTER INSERT ON messages BEGIN
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TRIGGER messages_ad AFTER DELETE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
END;

CREATE TRIGGER messages_au AFTER UPDATE ON messages BEGIN
    INSERT INTO messages_fts(messages_fts, rowid, body) VALUES('delete', old.rowid, old.body);
    INSERT INTO messages_fts(rowid, body) VALUES (new.rowid, new.body);
END;

CREATE TABLE contacts (
    jid           TEXT PRIMARY KEY,
    push_name     TEXT,
    full_name     TEXT,
    business_name TEXT
);

CREATE TABLE media (
    message_id TEXT PRIMARY KEY REFERENCES messages(id) ON DELETE CASCADE,
    mime       TEXT NOT NULL,
    size       INTEGER NOT NULL,
    sha256     TEXT NOT NULL,
    path       TEXT NOT NULL
);

CREATE TABLE events_log (
    seq     INTEGER PRIMARY KEY AUTOINCREMENT,
    ts      INTEGER NOT NULL,
    type    TEXT NOT NULL,
    payload TEXT NOT NULL
);

CREATE TABLE kv (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
