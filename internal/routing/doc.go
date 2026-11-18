// Package routing plans, orders, and measures pickup and dropoff routes.
//
// The balanced router prepares distances and household data, seeds routes with
// a bearing sweep or round-robin fallback, and tries to fill empty routes once
// all participants are assigned. It orders stops, then searches participant
// assignments, reordering stops after each accepted change. After checking for
// unassigned participants, it tries whole-route swaps between drivers. If any
// swap succeeds, that phase reruns stop ordering once. The router then builds
// the result.
//
// The solution score compares these criteria in order.
//
//  1. More drivers with riders.
//  2. Smaller sum of route corridor spreads, each rounded to a 10-degree bucket.
//  3. Shorter latest participant completion time.
//  4. Smaller maximum driver detour.
//  5. Shorter total participant completion time.
//  6. Shorter total drive duration.
//
// The first four criteria form the comparison prefix. The first two comparisons
// are exact. Each time comparison falls through at a difference of
// scoreImprovementEpsilon (0.001 seconds) or less. A full tie keeps the current
// solution. For pickups, participant completion measures time aboard.
//
// The swap phase rejects swaps that increase total drive duration by more than
// the tolerance. It discards the final reordering if total drive duration
// exceeds the value at the start of the swap phase by more than the tolerance.
package routing
