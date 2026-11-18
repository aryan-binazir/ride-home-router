// Package routing plans, orders, and measures pickup and dropoff routes.
//
// The balanced router prepares distances and household data, seeds routes with
// a bearing sweep or round-robin fallback, and tries to fill empty routes once
// all participants are assigned. It orders stops, searches participant
// assignments, checks for unassigned participants, tries whole-route swaps
// between drivers, and builds the result. A swap triggers another ordering pass.
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
// The ordering pass also uses the first four criteria to prune candidate stop
// orders. The first two comparisons are exact. Each time comparison falls
// through at a difference of 0.001 seconds or less. A full tie keeps the
// current solution. For pickups, participant completion measures time aboard.
package routing
