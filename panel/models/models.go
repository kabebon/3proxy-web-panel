package models

import "time"

type AdminUser struct {
	ID           int
	Username     string
	PasswordHash string
	CreatedAt    time.Time
}

type Session struct {
	ID          string
	AdminUserID int
	ExpiresAt   time.Time
	CreatedAt   time.Time
}

type Upstream struct {
	ID        int
	Name      string
	Type      string // "http" or "socks5"
	Host      string
	Port      int
	Username  string
	Password  string
	Enabled   bool
	CreatedAt time.Time
}

type UpstreamGroup struct {
	ID        int
	Name      string
	Enabled   bool
	Members   []UpstreamGroupMember
	CreatedAt time.Time
}

type UpstreamGroupMember struct {
	ID         int
	GroupID    int
	UpstreamID int
	Weight     int
	Upstream   *Upstream
}

type Listener struct {
	ID              int
	Name            string
	Protocol        string // "http" or "socks5"
	Port            int
	BindIP          string
	UpstreamID      *int
	Upstream        *Upstream
	UpstreamGroupID *int
	UpstreamGroup   *UpstreamGroup
	Enabled         bool
	CreatedAt       time.Time
}

type ProxyUser struct {
	ID           int
	Username     string
	Password     string
	ListenerID   *int
	Listener     *Listener
	BandwidthIn  int // KB/s, 0=unlimited
	BandwidthOut int // KB/s, 0=unlimited
	AllowedIPs   string
	Enabled      bool
	ExpiresAt    *time.Time
	CreatedAt    time.Time
}
