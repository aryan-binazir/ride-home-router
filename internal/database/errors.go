package database

import "errors"

// ErrNotFound is returned when a requested entity does not exist
var ErrNotFound = errors.New("entity not found")

// ErrCacheMiss is returned when a cache entry does not exist
var ErrCacheMiss = errors.New("cache miss")
