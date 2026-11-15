package app

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

var errNotConfigured = errors.New("Jellyfin is not configured")

type config struct {
	URL     string
	Token   string
	Refresh time.Duration
}

func loadConfig() (config, error) {
	c := config{URL: strings.TrimSpace(os.Getenv("JELLYFIN_URL")), Token: strings.TrimSpace(os.Getenv("JELLYFIN_TOKEN")), Refresh: 30 * time.Second}
	if c.URL == "" && c.Token == "" {
		return config{}, errNotConfigured
	}
	if raw := strings.TrimSpace(os.Getenv("JELLYFIN_REFRESH_INTERVAL")); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return config{}, fmt.Errorf("JELLYFIN_REFRESH_INTERVAL must be a positive duration")
		}
		c.Refresh = d
	}
	if c.URL == "" {
		return config{}, fmt.Errorf("JELLYFIN_URL is required")
	}
	if c.Token == "" {
		return config{}, fmt.Errorf("JELLYFIN_TOKEN is required")
	}
	return c, nil
}
