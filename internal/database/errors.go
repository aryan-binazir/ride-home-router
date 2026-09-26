package database

import "errors"

var ErrNotFound = errors.New("entity not found")

var ErrCacheMiss = errors.New("cache miss")

var ErrDuplicate = errors.New("entity already exists")
