# Ride Home Router

Pickup and dropoff route planning for events.

## Language

A **plan** is the choice of activity location, riders, drivers, van assignments, time, and pickup or dropoff mode used to [calculate routes](internal/handlers/routes.go).

The **roster** holds the [riders, drivers, and activity locations](internal/postgres/roster_writes.go) available for planning.

A **rider**, called a `Participant` in the [model](internal/models/models.go), needs a ride.

A [**driver**](internal/models/models.go) provides a ride and has an address and a vehicle capacity.

A **label** is a named grouping used to [filter or select](internal/handlers/planner_picker.go) riders and drivers.

A **household** is a group of riders the router tries to keep together. The [household key](internal/routing/balanced_households.go) uses normalized address and rounded coordinates when available.

A **van**, called an [`OrganizationVehicle`](internal/models/models.go) in code, is a shared vehicle that can be assigned to a selected driver. Its capacity [replaces the driver's personal vehicle capacity](internal/handlers/org_vehicle_assignments.go) for that plan.

A [**workflow**](internal/database/workflows.go) is an expiring record for in-progress work, either a route session or a roster import.

A [**route session**](internal/routesession/store.go) holds a calculated itinerary that can be edited before saving an event.

An [**import job**](internal/importer/persistent.go) is a unit of geocoding work for one distinct address in an import, shared by the rows that need that lookup.

An **estimated** distance or duration comes from the [planner's coordinate-based estimator](internal/distance/estimate.go). A **measured** timing or distance comes from [Google Compute Routes](internal/routing/measured.go) for an already ordered route. The live response can show measurements, and new events omit them.

An [**event**](internal/models/models.go) is a dated record of saved routes with notes and rider and driver snapshots.

A **handoff** is the instruction text for sharing a route. The driver version includes addresses and Maps links. The parent version lists the route's riders and activity location. See [handoff formatting](docs/architecture.md#where-to-change-things).
