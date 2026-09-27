CREATE TABLE IF NOT EXISTS upstream_health (
    upstream_id INTEGER PRIMARY KEY REFERENCES upstreams(id) ON DELETE CASCADE,
    status VARCHAR(10) NOT NULL DEFAULT 'up' CHECK (status IN ('up', 'down')),
    fail_count INTEGER NOT NULL DEFAULT 0,
    last_checked_at TIMESTAMPTZ,
    last_up_at TIMESTAMPTZ,
    last_down_at TIMESTAMPTZ,
    last_error TEXT DEFAULT ''
);
