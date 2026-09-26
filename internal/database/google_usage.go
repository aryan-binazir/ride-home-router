package database

import (
	"context"
	"errors"
)

type UsageSKU string

const (
	UsageSKURoutes       UsageSKU = "routes"
	UsageSKUGeocoding    UsageSKU = "geocoding"
	UsageSKUAutocomplete UsageSKU = "autocomplete"

	googleEssentialsMonthlyFreeRequests = 10000
	usageCeilingHeadroom                = 2000
	UsageDefaultCeiling                 = googleEssentialsMonthlyFreeRequests - usageCeilingHeadroom
)

var ErrUsageExhausted = errors.New("google usage ceiling reached for this month")

// GoogleUsageLedger reserves provider attempts before they are dispatched so
// spend stays inside the free tier by construction. Reservations are never
// released: a failed or abandoned attempt still counts.
type GoogleUsageLedger interface {
	Reserve(ctx context.Context, sku UsageSKU, attempts int) error
	Reserved(ctx context.Context, sku UsageSKU) (int, error)
	SetCeiling(ctx context.Context, sku UsageSKU, ceiling int) error
	Seed(ctx context.Context, sku UsageSKU, alreadyUsed int) error
}
