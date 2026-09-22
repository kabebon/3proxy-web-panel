CREATE TABLE IF NOT EXISTS admin_users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    password_hash VARCHAR(255) NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS sessions (
    id VARCHAR(64) PRIMARY KEY,
    admin_user_id INTEGER REFERENCES admin_users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS upstreams (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    type VARCHAR(10) NOT NULL CHECK (type IN ('http', 'socks5')),
    host VARCHAR(255) NOT NULL,
    port INTEGER NOT NULL,
    username VARCHAR(100) DEFAULT '',
    password VARCHAR(255) DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS upstream_groups (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS upstream_group_members (
    id SERIAL PRIMARY KEY,
    group_id INTEGER REFERENCES upstream_groups(id) ON DELETE CASCADE,
    upstream_id INTEGER REFERENCES upstreams(id) ON DELETE CASCADE,
    weight INTEGER DEFAULT 1,
    UNIQUE(group_id, upstream_id)
);

CREATE TABLE IF NOT EXISTS listeners (
    id SERIAL PRIMARY KEY,
    name VARCHAR(100) NOT NULL,
    protocol VARCHAR(10) NOT NULL CHECK (protocol IN ('http', 'socks5')),
    port INTEGER UNIQUE NOT NULL,
    bind_ip VARCHAR(45) DEFAULT '0.0.0.0',
    upstream_id INTEGER REFERENCES upstreams(id) ON DELETE SET NULL,
    upstream_group_id INTEGER REFERENCES upstream_groups(id) ON DELETE SET NULL,
    enabled BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS proxy_users (
    id SERIAL PRIMARY KEY,
    username VARCHAR(100) UNIQUE NOT NULL,
    password VARCHAR(255) NOT NULL,
    listener_id INTEGER REFERENCES listeners(id) ON DELETE SET NULL,
    bandwidth_in INTEGER DEFAULT 0,
    bandwidth_out INTEGER DEFAULT 0,
    allowed_ips TEXT DEFAULT '',
    enabled BOOLEAN DEFAULT TRUE,
    expires_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);
