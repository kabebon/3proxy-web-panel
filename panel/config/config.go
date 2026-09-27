package config

import (
	"os"
	"strconv"
	"strings"
)

type Config struct {
	DBUrl              string
	Port               string
	AdminUser          string
	AdminPassword      string
	SecretKey          string
	ProxyContainerName string
	ProxyConfigPath    string
	ProxyLogPath       string
	APITokens          []string
	PublicHost         string
	ReaperSeconds      int
	HealthcheckMode    string   // off | monitor | auto
	HealthcheckSeconds int      // interval between check rounds
	HealthcheckTimeout int      // per-target probe timeout, seconds
	HealthcheckFails   int      // consecutive failed rounds before DOWN
	HealthcheckTargets []string // host:port AI endpoints probed through each upstream
	TelegramBotToken   string
	TelegramChatID     string
}

func Load() *Config {
	c := &Config{
		DBUrl:              os.Getenv("DB_URL"),
		Port:               os.Getenv("PORT"),
		AdminUser:          os.Getenv("ADMIN_USER"),
		AdminPassword:      os.Getenv("ADMIN_PASSWORD"),
		SecretKey:          os.Getenv("SECRET_KEY"),
		ProxyContainerName: os.Getenv("PROXY_CONTAINER_NAME"),
		ProxyConfigPath:    os.Getenv("PROXY_CONFIG_PATH"),
		ProxyLogPath:       os.Getenv("PROXY_LOG_PATH"),
		PublicHost:         os.Getenv("PUBLIC_HOST"),
		ReaperSeconds:      30,
		HealthcheckMode:    "auto",
		HealthcheckSeconds: 30,
		HealthcheckTimeout: 10,
		HealthcheckFails:   1,
		HealthcheckTargets: []string{"api.openai.com:443", "api.x.ai:443"},
		TelegramBotToken:   os.Getenv("TELEGRAM_BOT_TOKEN"),
		TelegramChatID:     os.Getenv("TELEGRAM_CHAT_ID"),
	}

	for _, t := range strings.Split(os.Getenv("API_TOKENS"), ",") {
		if t = strings.TrimSpace(t); t != "" {
			c.APITokens = append(c.APITokens, t)
		}
	}
	if n, err := strconv.Atoi(os.Getenv("REAPER_SECONDS")); err == nil && n > 0 {
		c.ReaperSeconds = n
	}
	switch strings.ToLower(strings.TrimSpace(os.Getenv("HEALTHCHECK_MODE"))) {
	case "off", "monitor", "auto":
		c.HealthcheckMode = strings.ToLower(strings.TrimSpace(os.Getenv("HEALTHCHECK_MODE")))
	case "":
		// keep default (auto)
	default:
		// unknown value: fail safe to monitor (no automatic switching)
		c.HealthcheckMode = "monitor"
	}
	if n, err := strconv.Atoi(os.Getenv("HEALTHCHECK_SECONDS")); err == nil && n >= 5 {
		c.HealthcheckSeconds = n
	}
	if n, err := strconv.Atoi(os.Getenv("HEALTHCHECK_TIMEOUT")); err == nil && n >= 2 {
		c.HealthcheckTimeout = n
	}
	if n, err := strconv.Atoi(os.Getenv("HEALTHCHECK_FAILS")); err == nil && n >= 1 {
		c.HealthcheckFails = n
	}
	if v := os.Getenv("HEALTHCHECK_TARGETS"); strings.TrimSpace(v) != "" {
		var targets []string
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" {
				targets = append(targets, t)
			}
		}
		if len(targets) > 0 {
			c.HealthcheckTargets = targets
		}
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
