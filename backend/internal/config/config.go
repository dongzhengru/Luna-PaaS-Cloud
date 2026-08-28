package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr        string
	DatabaseURL string
	PublicURL   string
	FrontendURL string

	OAuthAuthURL      string
	OAuthTokenURL     string
	OAuthUserURL      string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthRedirectURL  string
	OAuthScope        string
	OAuthPhonePath    string

	Registry          string
	RegistryNamespace string
	MasterKey         []byte
	SecureCookies     bool
	SessionTTL        time.Duration
}

func Load() (Config, error) {
	c := Config{
		Addr:              env("PAAS_ADDR", ":8080"),
		DatabaseURL:       os.Getenv("PAAS_DATABASE_URL"),
		PublicURL:         strings.TrimRight(os.Getenv("PAAS_PUBLIC_URL"), "/"),
		FrontendURL:       strings.TrimRight(env("PAAS_FRONTEND_URL", "http://localhost:5173"), "/"),
		OAuthAuthURL:      env("OAUTH_AUTH_URL", "https://id.zhengru.top/oauth2/authorize"),
		OAuthTokenURL:     env("OAUTH_TOKEN_URL", "https://id.zhengru.top/api/oauth2/token"),
		OAuthUserURL:      env("OAUTH_USER_URL", "https://id.zhengru.top/api/user"),
		OAuthClientID:     os.Getenv("OAUTH_CLIENT_ID"),
		OAuthClientSecret: os.Getenv("OAUTH_CLIENT_SECRET"),
		OAuthRedirectURL:  os.Getenv("OAUTH_REDIRECT_URL"),
		OAuthScope:        env("OAUTH_SCOPE", "openid profile"),
		OAuthPhonePath:    env("OAUTH_PHONE_PATH", "phone"),
		Registry: 		   os.Getenv("ACR_REGISTRY"),
		RegistryNamespace: os.Getenv("ACR_NAMESPACE"),
		SecureCookies:     envBool("PAAS_SECURE_COOKIES", true),
		SessionTTL:        12 * time.Hour,
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("PAAS_DATABASE_URL is required")
	}
	raw := os.Getenv("PAAS_MASTER_KEY")
	key, err := base64.StdEncoding.DecodeString(raw)
	if err != nil || len(key) != 32 {
		return c, fmt.Errorf("PAAS_MASTER_KEY must be base64 for exactly 32 bytes")
	}
	c.MasterKey = key
	return c, nil
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func envBool(k string, d bool) bool {
	v := os.Getenv(k)
	if v == "" {
		return d
	}
	b, e := strconv.ParseBool(v)
	if e != nil {
		return d
	}
	return b
}
