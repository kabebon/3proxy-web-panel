-- Parser offsets for the log-based traffic accounter (stage 3).
-- Updated in the same transaction as the per-user counters, so a crash can
-- neither lose nor double-count bytes. Empty table = the accounter never ran
-- (it then seeds offsets to EOF, counting only traffic from deployment
-- onward instead of billing history).
CREATE TABLE IF NOT EXISTS traffic_accounting_state (
    file_name TEXT PRIMARY KEY,
    read_offset BIGINT NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
