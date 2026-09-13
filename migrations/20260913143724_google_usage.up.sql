-- Monthly reservation counters for Google Maps Platform requests, one row per
-- UTC calendar month and SKU. Counts are attempts reserved before dispatch and
-- are never released, so the app stays inside the free tier by construction.
CREATE TABLE google_usage (
    month_start date NOT NULL,
    sku text NOT NULL,
    reserved integer NOT NULL DEFAULT 0 CHECK (reserved >= 0),
    ceiling integer NOT NULL CHECK (ceiling >= 0),
    PRIMARY KEY (month_start, sku),
    CHECK (reserved <= ceiling)
);
