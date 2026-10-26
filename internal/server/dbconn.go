package server

import (
	"database/sql"
	"fmt"
	"os"

	_ "github.com/lib/pq"
)

type LocalDBConnection struct {
	User   string
	DBName string
}

const (
	Driver = "postgres"
)

func CreateDBConnection(conn LocalDBConnection) (*sql.DB, error) {
	var dataSource string

	// When deployed in production on Heroku, the DATABASE_URL environment will
	// be set on Heroku machines, and we don't need to manually create the
	// database URL. For more:
	// https://devcenter.heroku.com/articles/heroku-postgresql#provisioning-heroku-postgres
	if dbURL, ok := os.LookupEnv("DATABASE_URL"); ok {
		dataSource = dbURL
	} else {
		dataSource = createLocalDBUrl(conn)
	}

	db, err := sql.Open(Driver, dataSource)
	if err != nil {
		db.Close()
		return db, err
	}

	return db, nil
}

func createLocalDBUrl(conn LocalDBConnection) string {
	return fmt.Sprintf(
		"user=%s dbname=%s sslmode=disable",
		conn.User,
		conn.DBName,
	)
}
