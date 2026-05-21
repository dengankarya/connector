package dbconn

import (
	"context"
	"database/sql"
	"sync"
	"time"

	_ "github.com/lib/pq"
	log "github.com/sirupsen/logrus"
)

var (
	db   *sql.DB
	once sync.Once
)

// Connect initializes the database connection pool once.
func Connect(DSN string) *sql.DB {
	once.Do(func() {
		conn, err := sql.Open("postgres", DSN)
		if err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Fatal("failed to open database connection")
		}

		// Pool configuration
		conn.SetMaxOpenConns(25)
		conn.SetMaxIdleConns(25)
		conn.SetConnMaxLifetime(5 * time.Minute)
		conn.SetConnMaxIdleTime(2 * time.Minute)

		// Verify connection
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := conn.PingContext(ctx); err != nil {
			log.WithFields(log.Fields{
				"error": err,
			}).Fatal("failed to open ping connection")
		}

		db = conn
	})

	log.Info("database connection established")
	return db
}
