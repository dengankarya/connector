// cmd/migrate runs database migrations using golang-migrate.
// Usage:
//
//	go run cmd/migrate/main.go up           # apply all pending migrations
//	go run cmd/migrate/main.go down         # roll back one migration
//	go run cmd/migrate/main.go down 0       # roll back all migrations
//	go run cmd/migrate/main.go version      # print current version
//	go run cmd/migrate/main.go force 1      # force-set version (use after manual fix)
//
// DATABASE_DSN must be set in the environment or .env file.
package main

import (
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/joho/godotenv/autoload"
	log "github.com/sirupsen/logrus"
)

func main() {
	log.SetFormatter(&log.TextFormatter{FullTimestamp: true})

	dsn := os.Getenv("DATABASE_DSN")
	if dsn == "" {
		log.Fatal("DATABASE_DSN environment variable is not set")
	}

	m, err := migrate.New("file://db/migrations", dsn)
	if err != nil {
		log.WithError(err).Fatal("failed to initialise migrate")
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			log.WithError(srcErr).Warn("migrate source close error")
		}
		if dbErr != nil {
			log.WithError(dbErr).Warn("migrate db close error")
		}
	}()

	cmd := "up"
	if len(os.Args) > 1 {
		cmd = os.Args[1]
	}

	switch cmd {
	case "up":
		if err := m.Up(); err != nil && err != migrate.ErrNoChange {
			log.WithError(err).Fatal("migrate up failed")
		}
		v, _, _ := m.Version()
		log.WithField("version", v).Info("migrate up: done")

	case "down":
		steps := 1
		if len(os.Args) > 2 {
			n, err := strconv.Atoi(os.Args[2])
			if err != nil {
				log.Fatalf("invalid steps %q: %v", os.Args[2], err)
			}
			if n == 0 {
				// Roll back everything.
				if err := m.Down(); err != nil && err != migrate.ErrNoChange {
					log.WithError(err).Fatal("migrate down all failed")
				}
				log.Info("migrate down: all migrations rolled back")
				return
			}
			steps = n
		}
		if err := m.Steps(-steps); err != nil && err != migrate.ErrNoChange {
			log.WithError(err).Fatal("migrate down failed")
		}
		v, _, _ := m.Version()
		log.WithField("version", v).Info("migrate down: done")

	case "version":
		v, dirty, err := m.Version()
		if err != nil && err != migrate.ErrNilVersion {
			log.WithError(err).Fatal("migrate version failed")
		}
		fmt.Printf("version=%d dirty=%v\n", v, dirty)

	case "force":
		if len(os.Args) < 3 {
			log.Fatal("force requires a version argument: migrate force <version>")
		}
		v, err := strconv.Atoi(os.Args[2])
		if err != nil {
			log.Fatalf("invalid version %q: %v", os.Args[2], err)
		}
		if err := m.Force(v); err != nil {
			log.WithError(err).Fatal("migrate force failed")
		}
		log.WithField("version", v).Info("migrate force: done")

	default:
		log.Fatalf("unknown command %q — use: up | down [steps] | version | force <version>", cmd)
	}
}
