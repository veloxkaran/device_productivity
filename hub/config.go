package hub

import (
	"crypto/rand"
	"encoding/hex"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	_ "time/tzdata"
)

type Config struct {
	Addr               string
	DataDir            string
	HajirAPIURL        string
	AllowedOrigins     []string
	Location           *time.Location
	Secret             []byte
	OnlineThreshold    time.Duration
	ScreenshotRetainDy int
	SampleRetainDays   int
	PublicURL          string
	InternalKey        string // shared with Laravel (DEVICE_HUB_KEY) for /api/internal/*
}

func env(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envInt(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil && v > 0 {
		return v
	}
	return def
}

func LoadConfig() (Config, error) {
	cfg := Config{
		Addr:               env("HUB_ADDR", ":4010"),
		DataDir:            env("HUB_DATA_DIR", "hub-data"),
		HajirAPIURL:        strings.TrimRight(env("HAJIR_API_URL", "http://localhost:8001/api/v2"), "/"),
		OnlineThreshold:    time.Duration(envInt("HUB_ONLINE_THRESHOLD_SECONDS", 90)) * time.Second,
		ScreenshotRetainDy: envInt("HUB_SCREENSHOT_RETENTION_DAYS", 30),
		SampleRetainDays:   envInt("HUB_SAMPLE_RETENTION_DAYS", 90),
		PublicURL:          strings.TrimRight(env("HUB_PUBLIC_URL", "http://localhost:4010"), "/"),
		InternalKey:        env("HUB_INTERNAL_KEY", ""),
	}
	for _, o := range strings.Split(env("HUB_ALLOWED_ORIGINS", "http://localhost:3000"), ",") {
		if o = strings.TrimSpace(o); o != "" {
			cfg.AllowedOrigins = append(cfg.AllowedOrigins, o)
		}
	}
	loc, err := time.LoadLocation(env("HUB_TIMEZONE", "Asia/Kathmandu"))
	if err != nil {
		return cfg, err
	}
	cfg.Location = loc
	if err := os.MkdirAll(filepath.Join(cfg.DataDir, "screenshots"), 0755); err != nil {
		return cfg, err
	}
	secret, err := loadSecret(cfg.DataDir)
	if err != nil {
		return cfg, err
	}
	cfg.Secret = secret
	return cfg, nil
}

func loadSecret(dir string) ([]byte, error) {
	if v := os.Getenv("HUB_SECRET"); v != "" {
		return []byte(v), nil
	}
	path := filepath.Join(dir, "secret.key")
	if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
		return b, nil
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return nil, err
	}
	s := []byte(hex.EncodeToString(buf))
	return s, os.WriteFile(path, s, 0600)
}
