-- ride-home-router: down migration disabled

DROP TABLE IF EXISTS distance_cache;
DROP TABLE IF EXISTS event_summaries;
DROP TABLE IF EXISTS event_route_stops;
DROP TABLE IF EXISTS event_routes;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS settings;
DROP TABLE IF EXISTS driver_labels;
DROP TABLE IF EXISTS participant_labels;
DROP TABLE IF EXISTS labels;
DROP TABLE IF EXISTS organization_vehicles;
DROP TABLE IF EXISTS drivers;
DROP TABLE IF EXISTS participants;
DROP TABLE IF EXISTS activity_locations;
