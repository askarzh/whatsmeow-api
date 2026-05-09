CREATE TABLE receipts (
    message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    reader_jid TEXT NOT NULL,
    type       TEXT NOT NULL,
    ts         TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (message_id, reader_jid, type)
);
CREATE INDEX idx_receipts_message ON receipts (message_id);
