package config

import "os"

type Config struct {
	DBUrl              string
	AdminUser          string
	AdminPassword      string
	SecretKey          string
	Port               string
	ProxyContainerName string
	ProxyConfigPath    string
	ProxyLogPath       string
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
