package app

import (
	"errors"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Config struct {
	Origin, DatabaseURL, ClientID, ClientSecret, StaticDir string
	OwnerID                                                int64
	BotUsername, BotToken                                  string
}

func LoadConfig() (Config, error) {
	c := Config{Origin: os.Getenv("APP_ORIGIN"), DatabaseURL: os.Getenv("DATABASE_URL"), ClientID: os.Getenv("TELEGRAM_CLIENT_ID"), ClientSecret: os.Getenv("TELEGRAM_CLIENT_SECRET"), StaticDir: os.Getenv("STATIC_DIR")}
	c.OwnerID, _ = strconv.ParseInt(os.Getenv("OWNER_TELEGRAM_ID"), 10, 64)
	if err := validateOrigin(c.Origin); err != nil {
		return c, err
	}
	if c.OwnerID <= 0 || strings.TrimSpace(c.ClientID) == "" || strings.TrimSpace(c.ClientSecret) == "" || c.DatabaseURL == "" {
		return c, errors.New("DATABASE_URL, positive OWNER_TELEGRAM_ID and Telegram client credentials are required")
	}
	if c.StaticDir == "" {
		c.StaticDir = "frontend/dist"
	}
	c.BotUsername = os.Getenv("TELEGRAM_BOT_USERNAME")
	if !botUsernamePattern.MatchString(c.BotUsername) {
		return c, errors.New("TELEGRAM_BOT_USERNAME must be a bot username without @")
	}
	return c, nil
}

var botUsernamePattern = regexp.MustCompile(`(?i)^[a-z][a-z0-9_]{1,28}bot$`)
var botTokenPattern = regexp.MustCompile(`^[0-9]+:[A-Za-z0-9_-]+$`)

func LoadBotConfig() (Config, error) {
	c := Config{DatabaseURL: os.Getenv("DATABASE_URL"), BotUsername: os.Getenv("TELEGRAM_BOT_USERNAME"), BotToken: os.Getenv("TELEGRAM_BOT_TOKEN")}
	c.OwnerID, _ = strconv.ParseInt(os.Getenv("OWNER_TELEGRAM_ID"), 10, 64)
	if c.DatabaseURL == "" || c.OwnerID <= 0 || !botUsernamePattern.MatchString(c.BotUsername) || !botTokenPattern.MatchString(c.BotToken) {
		return c, errors.New("DATABASE_URL, positive OWNER_TELEGRAM_ID, TELEGRAM_BOT_USERNAME and TELEGRAM_BOT_TOKEN are required")
	}
	return c, nil
}

func validateOrigin(origin string) error {
	u, err := url.Parse(origin)
	if err != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Path != "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1"))) {
		return errors.New("APP_ORIGIN must be an HTTPS origin (HTTP allowed for loopback only)")
	}
	return nil
}

func LoadDigestConfig() (Config, error) {
	c, err := LoadBotConfig()
	if err != nil {
		return c, err
	}
	c.Origin = os.Getenv("APP_ORIGIN")
	if err := validateOrigin(c.Origin); err != nil {
		return c, err
	}
	// Leave ample room for the compact summary within Telegram's text limit.
	if digestLength(c.Origin) > 2048 {
		return c, errors.New("APP_ORIGIN must fit within 2048 UTF-16 units")
	}
	return c, nil
}
