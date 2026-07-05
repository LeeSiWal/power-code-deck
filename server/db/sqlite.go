package db

import (
	"database/sql"
	"log"

	// Pure-Go SQLite driver (no cgo) — lets pcd build without a C toolchain and
	// cross-compile to native Windows.
	_ "modernc.org/sqlite"
)

func Init(dbPath string) *sql.DB {
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)&_pragma=busy_timeout(5000)")
	if err != nil {
		log.Fatalf("Failed to open database: %v", err)
	}

	db.SetMaxOpenConns(2)

	if err := db.Ping(); err != nil {
		log.Fatalf("Failed to ping database: %v", err)
	}

	if err := Migrate(db); err != nil {
		log.Fatalf("Failed to run migrations: %v", err)
	}

	log.Println("Database initialized:", dbPath)
	return db
}
