package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBUrl              string
	AdminUser          string
	AdminPassword      string
	SecretKey          string
	Port               string
	ProxyContainerName string
	ProxyConfigPath    string
	ProxyLogPath       string
	APITokens          []string
	PublicHost         string
	ReaperSeconds      int
}

func Load() *Config {
	c := &Config{
		DBUrl:              os.Getenv("DB_URL"),
		AdminUser:          os.Getenv("ADMIN_USER"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		SecretKey:          os.Getenv("SECRET_KEY"),
		Port:               os.Getenv("PORT"),
		ProxyContainerName: os.Getenv("PROXY_CONTAINER_NAME"),
		ProxyConfigPath:    os.Getenv("PROXY_CONFIG_PATH"),
		ProxyLogPath:       os.Getenv("PROXY_LOG_PATH"),
		PublicHost:         os.Getenv("PUBLIC_HOST"),
		ReaperSeconds:      30,
	}

	for _, t := range strings.Split(os.Getenv("API_TOKENS"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			c.APITokens = append(c.APITokens, t)
		}
	}
	if n, err := strconv.Atoi(os.Getenv("REAPER_SECONDS")); err == nil && n > 0 {
		c.ReaperSeconds = n
	}

	if c.AdminUser == "" {
		c.AdminUser = "admin"
	}
	if c.AdminPassword == "" {
		c.AdminPassword = "changeme"
	}
	if c.Port == "" {
		c.Port = "8080"
	}
	if c.ProxyContainerName == "" {
		c.ProxyContainerName = "3proxy"
	}
	if c.ProxyConfigPath == "" {
		c.ProxyConfigPath = "/etc/3proxy/3proxy.cfg"
	}
	if c.ProxyLogPath == "" {
		c.ProxyLogPath = "/var/log/3proxy/3proxy.log"
	}
	return c
}
