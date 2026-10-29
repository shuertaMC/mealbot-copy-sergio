// Package mealbot exposes assets shipped at the root of the repository.
package mealbot

import _ "embed"

// Schema is the contents of schema.sql: it (re)creates every table the service uses.
// WARNING: it drops the existing tables first.
//
//go:embed schema.sql
var Schema string
