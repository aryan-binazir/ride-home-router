CREATE TABLE IF NOT EXISTS local_dev_seed (version integer PRIMARY KEY);
DO $$ BEGIN
IF NOT EXISTS (SELECT 1 FROM local_dev_seed) THEN
INSERT INTO approved_emails(email) VALUES ('member@example.test');
INSERT INTO activity_locations(name,address,lat,lng,geocoded_at) VALUES
('Synthetic community center','100 Example Plaza, Durham NC',35.965,-78.945,now()),
('Synthetic park','200 Example Plaza, Durham NC',35.97,-78.94,now());
INSERT INTO activity_locations(name,address,lat,lng,geocoded_at) SELECT 'Synthetic place '||i,(300+i)||' Example Plaza, Durham NC',35.96+i*0.0001,-78.94,now() FROM generate_series(3,26) AS i;
UPDATE settings SET selected_activity_location_id=(SELECT min(id) FROM activity_locations);
INSERT INTO participants(name,address,address_name,lat,lng,geocoded_at)
SELECT 'Synthetic rider '||lpad(i::text,2,'0'), (100+(i-1)/3)||' Example Lane, Durham NC',
'Household '||((i-1)/3+1),35.96+(((i-1)/3)%12)*0.0007,-78.95+(((i-1)/3)/12)*0.001,now() FROM generate_series(1,72) AS i;
INSERT INTO drivers(name,address,lat,lng,vehicle_capacity,geocoded_at)
SELECT 'Synthetic driver '||lpad(i::text,2,'0'),(200+i)||' Example Drive, Durham NC',35.962+(i%8)*0.001,-78.95+(i/8)*0.002,
CASE WHEN i=1 THEN 1 WHEN i=2 THEN 2 ELSE 6 END,now() FROM generate_series(1,56) AS i;
INSERT INTO organization_vehicles(name,capacity) VALUES ('Synthetic compact',2),('Synthetic van',8),('Synthetic minibus',16);
INSERT INTO organization_vehicles(name,capacity) SELECT 'Synthetic vehicle '||i,4 FROM generate_series(4,26) AS i;
INSERT INTO labels(name) VALUES ('Synthetic youth group'),('Synthetic neighbors');
INSERT INTO participant_labels SELECT (SELECT min(id) FROM labels),id FROM participants WHERE id%2=0;
INSERT INTO driver_labels SELECT (SELECT min(id) FROM labels),id FROM drivers WHERE id%2=0;
INSERT INTO events(event_date,notes,mode) SELECT current_date-i,'Synthetic history '||i||'. No real travel measurements.','dropoff' FROM generate_series(1,24) AS i;
INSERT INTO event_routes(event_id,route_order,driver_id,driver_name,driver_address,effective_capacity,mode)
SELECT e.id,1,d.id,d.name,d.address,6,'dropoff' FROM events e CROSS JOIN (SELECT * FROM drivers ORDER BY id LIMIT 1 OFFSET 2) d;
INSERT INTO event_route_stops(event_route_id,route_order,participant_id,participant_name,participant_address)
SELECT r.id,1,p.id,p.name,p.address FROM event_routes r CROSS JOIN (SELECT * FROM participants ORDER BY id LIMIT 1) p;
INSERT INTO event_summaries(event_id,total_participants,total_drivers,mode) SELECT id,1,1,'dropoff' FROM events;
INSERT INTO local_dev_seed VALUES (1);
END IF;
END $$;
