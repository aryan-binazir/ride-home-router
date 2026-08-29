package database

import "errors"

// ErrNotFound indicates that an entity does not exist.
var ErrNotFound = errors.New("entity not found")

// ErrCacheMiss indicates that a cache entry does not exist.
var ErrCacheMiss = errors.New("cache miss")

// ErrDuplicate indicates a uniqueness violation.
var ErrDuplicate = errors.New("entity already exists")
