ALTER TABLE proxy_users ADD COLUMN traffic_limit BIGINT DEFAULT 0;
ALTER TABLE proxy_users ADD COLUMN traffic_used BIGINT DEFAULT 0;