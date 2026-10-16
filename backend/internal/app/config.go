package app

import (
	"errors"
	"net/url"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Origin, DatabaseURL, ClientID, ClientSecret, StaticDir string
	OwnerID                                                int64
}

func LoadConfig() (Config, error) {
	c := Config{Origin: os.Getenv("APP_ORIGIN"), DatabaseURL: os.Getenv("DATABASE_URL"), ClientID: os.Getenv("TELEGRAM_CLIENT_ID"), ClientSecret: os.Getenv("TELEGRAM_CLIENT_SECRET"), StaticDir: os.Getenv("STATIC_DIR")}
	c.OwnerID, _ = strconv.ParseInt(os.Getenv("OWNER_TELEGRAM_ID"), 10, 64)
	u, err := url.Parse(c.Origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return c, errors.New("APP_ORIGIN must be an HTTPS origin (HTTP allowed for loopback only)")
	}
	if c.OwnerID <= 0 || strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" || c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL, positive OWNER_TELEGRAM_ID and Telegram client credentials are required")
	}
	if c.StaticDir == "" {
		c.StaticDir = "frontend/dist"
	}
	return c, nil
}
