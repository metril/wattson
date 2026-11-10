ALTER TABLE riders ADD COLUMN max_hr_bpm INTEGER CHECK (max_hr_bpm IS NULL OR max_hr_bpm BETWEEN 100 AND 230);

-- Snapshots taken when the ride starts; NULL on rides recorded before this migration until backfilled.
ALTER TABLE rides ADD COLUMN ftp_w INTEGER;
ALTER TABLE rides ADD COLUMN weight_kg REAL;
ALTER TABLE rides ADD COLUMN hr_max_bpm INTEGER;
ALTER TABLE rides ADD COLUMN route_distance_m REAL;
-- Derived metrics; NULL when they cannot be computed.
ALTER TABLE rides ADD COLUMN completed INTEGER NOT NULL DEFAULT 0;
ALTER TABLE rides ADD COLUMN intensity_factor REAL;
ALTER TABLE rides ADD COLUMN tss REAL;
ALTER TABLE rides ADD COLUMN variability_index REAL;
ALTER TABLE rides ADD COLUMN efficiency_factor REAL;
ALTER TABLE rides ADD COLUMN decoupling_pct REAL;
ALTER TABLE rides ADD COLUMN vam_mph REAL; -- metres of ascent per hour
ALTER TABLE rides ADD COLUMN max_cadence_rpm REAL NOT NULL DEFAULT 0;
ALTER TABLE rides ADD COLUMN stats_version INTEGER NOT NULL DEFAULT 0;

CREATE INDEX rides_rider_started ON rides(rider_id, started_at);
CREATE INDEX rides_rider_route_started ON rides(rider_id, route_id, started_at);

CREATE TABLE ride_power_curve (
    ride_id INTEGER NOT NULL REFERENCES rides(id) ON DELETE CASCADE,
    dur_s   INTEGER NOT NULL,
    watts   REAL NOT NULL,
    PRIMARY KEY (ride_id, dur_s)
) WITHOUT ROWID;

CREATE TABLE ride_zones (
    ride_id INTEGER NOT NULL REFERENCES rides(id) ON DELETE CASCADE,
    kind    TEXT NOT NULL CHECK (kind IN ('power','hr')),
    zone    INTEGER NOT NULL,
    seconds REAL NOT NULL,
    PRIMARY KEY (ride_id, kind, zone)
) WITHOUT ROWID;
