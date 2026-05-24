package config

import (
	"strings"

	"github.com/caarlos0/env/v11"
	_ "github.com/joho/godotenv/autoload"
	"github.com/sirupsen/logrus"
)

type Configuration struct {
	ENV            string `env:"ENV"`
	PORT           string `env:"PORT"`
	AllowedAPIKeys string `env:"ALLOWED_API_KEYS"`

	DatabaseDSN string `env:"DATABASE_DSN"`

	BiteshipAPIKey  string `env:"BITESHIP_API_KEY"`
	BiteshipBaseURL string `env:"BITESHIP_BASE_URL"`

	WilayahBaseURL string `env:"WILAYAH_BASE_URL,notEmpty" envDefault:"https://wilayah.id"`

	XenditAPIKey       string `env:"XENDIT_API_KEY"`
	XenditBaseURL      string `env:"XENDIT_BASE_URL,notEmpty" envDefault:"https://api.xendit.co"`
	XenditWebhookToken string `env:"XENDIT_WEBHOOK_TOKEN"`

	TokokaryaURL    string `env:"TOKOKARYA_URL"`
	TokokaryaAPIKey string `env:"TOKOKARYA_API_KEY"`

	RedisURL string `env:"REDIS_URL,notEmpty" envDefault:"redis://localhost:6379"`
}

func (c Configuration) IsThisRequestAuthenticated(apiKey string) bool {
	if apiKey == "" {
		return false
	}
	for _, key := range strings.Split(c.AllowedAPIKeys, ",") {
		if strings.TrimSpace(key) == apiKey {
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
