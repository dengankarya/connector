// cmd/seed creates a platform admin user in the admin_users table.
//
// Usage:
//
//	go run cmd/seed/main.go --email ops@tokokarya.id --password secretpassword
//
// DATABASE_DSN must be set in the environment or .env file.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/joho/godotenv/autoload"
	log "github.com/sirupsen/logrus"
	"golang.org/x/crypto/bcrypt"
)

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	email := flag.String("email", "", "admin user email (required)")
	password := flag.String("password", "", "admin user password (required)")
	flag.Parse()

	if *email == "" || *password == "" {
		fmt.Fprintln(os.Stderr, "usage: go run cmd/seed/main.go --email <email> --password <password>")
		os.Exit(1)
	}

	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		log.Fatal("DATABASE_DSN environment variable is not set")
	}

	ctx := context.Background()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		log.WithError(err).Fatal("failed to connect to database")
	}
	defer pool.Close()

	hash, err := bcrypt.GenerateFromPassword([]byte(*password), bcrypt.DefaultCost)
	if err != nil {
		log.WithError(err).Fatal("failed to hash password")
	}

	var id string
	err = pool.QueryRow(ctx,
		`INSERT INTO admin_users (email, password_hash)
		 VALUES ($1, $2)
		 ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash, updated_at = NOW()
		 RETURNING id`,
		*email, string(hash),
	).Scan(&id)
	if err != nil {
		log.WithError(err).Fatal("failed to upsert admin user")
	}

	log.WithFields(log.Fields{"id": id, "email": *email}).Info("admin user upserted")
}
