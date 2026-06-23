package config

import (
	"crypto/subtle"
	"strings"

	"github.com/caarlos0/env/v11"
	_ "github.com/joho/godotenv/autoload"
	"github.com/sirupsen/logrus"
)

type Configuration struct {
	ENV            string `env:"ENV"`
	PORT           string `env:"PORT"`
	AllowedAPIKeys string `env:"ALLOWED_API_KEYS"`
	AdminAPIKeys   string `env:"ADMIN_API_KEYS"` // operator-only endpoints (topup, payouts, etc.)

	DatabaseDSN string `env:"DATABASE_DSN"`

	BiteshipAPIKey                string `env:"BITESHIP_API_KEY"`
	BiteshipBaseURL               string `env:"BITESHIP_BASE_URL"`
	BiteshipWebhookSignatureKey   string `env:"BITESHIP_WEBHOOK_SIGNATURE_KEY"`
	BiteshipWebhookSignatureValue string `env:"BITESHIP_WEBHOOK_SIGNATURE_VALUE"`

	WilayahBaseURL string `env:"WILAYAH_BASE_URL,notEmpty" envDefault:"https://wilayah.id"`

	TokokaryaURL    string `env:"TOKOKARYA_URL"`
	TokokaryaAPIKey string `env:"TOKOKARYA_API_KEY"`

	RedisURL string `env:"REDIS_URL,notEmpty" envDefault:"redis://localhost:6379"`

	GeoapifyAPIKey  string `env:"GEOAPIFY_API_KEY"`
	GeoapifyBaseURL string `env:"GEOAPIFY_BASE_URL"`

}

func (c Configuration) IsThisRequestAuthenticated(apiKey string) bool {
	if apiKey == "" {
		return false
	}
	// Admin keys are implicitly valid regular keys — no need to list them in both vars.
	for key := range strings.SplitSeq(c.AllowedAPIKeys+","+c.AdminAPIKeys, ",") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(key)), []byte(apiKey)) == 1 {
			return true
		}
	}

	return false
}

// IsAdminRequest returns true when the key is in ADMIN_API_KEYS.
// Admin keys are also valid regular keys — no need to list them in both vars.
func (c Configuration) IsAdminRequest(apiKey string) bool {
	if apiKey == "" || c.AdminAPIKeys == "" {
		return false
	}
	for key := range strings.SplitSeq(c.AdminAPIKeys, ",") {
		if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(key)), []byte(apiKey)) == 1 {
			return true
		}
	}
	return false
}

func ParseENV() *Configuration {
	cfg, err := env.ParseAs[Configuration]()
	if err != nil {
		logrus.WithFields(
			logrus.Fields{"error": err},
		).Fatal("failed to parse env to struct")
	}

	return &cfg
}
