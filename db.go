package main

import "github.com/johnamadeo/mealbot/internal/server"

// DuplicateKeyErr : Error message when user tries to insert row with duplicate key
const DuplicateKeyErr = "duplicate key value violates unique constraint"

// LocalDBConnection :
var LocalDBConnection = server.LocalDBConnection{
	User:   "johnamadeodaniswara",
	DBName: "mealbot",
}
