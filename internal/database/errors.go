package database

import "errors"

// ErrNotFound is returned when a requested entity does not exist
var ErrNotFound = errors.New("entity not found")
