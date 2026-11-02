CREATE TABLE rides (
    id                 INTEGER PRIMARY KEY,
    rider_id           INTEGER NOT NULL REFERENCES riders(id) ON DELETE CASCADE,
    route_id           INTEGER REFERENCES routes(id) ON DELETE SET NULL,
    route_name         TEXT NOT NULL DEFAULT '',
    mode               TEXT NOT NULL,
    started_at         TEXT NOT NULL,
    ended_at           TEXT,
    duration_s         REAL NOT NULL DEFAULT 0,
    elapsed_s          REAL NOT NULL DEFAULT 0,
    distance_m         REAL NOT NULL DEFAULT 0,
    ascent_m           REAL NOT NULL DEFAULT 0,
    avg_power_w        REAL NOT NULL DEFAULT 0,
    max_power_w        REAL NOT NULL DEFAULT 0,
    np_w               REAL NOT NULL DEFAULT 0,
    kj                 REAL NOT NULL DEFAULT 0,
    avg_cadence_rpm    REAL NOT NULL DEFAULT 0,
    avg_speed_kmh      REAL NOT NULL DEFAULT 0,
    max_speed_kmh      REAL NOT NULL DEFAULT 0,
    avg_hr_bpm         INTEGER NOT NULL DEFAULT 0,
    max_hr_bpm         INTEGER NOT NULL DEFAULT 0,
    sample_count       INTEGER NOT NULL DEFAULT 0,
    strava_status      TEXT NOT NULL DEFAULT 'none' CHECK (strava_status IN ('none','uploading','done','error')),
    strava_upload_id   INTEGER,
    strava_activity_id INTEGER,
    strava_error       TEXT NOT NULL DEFAULT '',
    strava_updated_at  TEXT
);
CREATE INDEX rides_rider_id ON rides(rider_id, id DESC);

CREATE TABLE samples (
    ride_id     INTEGER NOT NULL REFERENCES rides(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    at_ms       INTEGER NOT NULL,
    elapsed_s   REAL NOT NULL,
    distance_m  REAL NOT NULL,
    speed_kmh   REAL NOT NULL,
    power_w     REAL NOT NULL,
    cadence_rpm REAL NOT NULL,
    grade_pct   REAL NOT NULL,
    level       INTEGER NOT NULL,
    hr_bpm      INTEGER,
    lat         REAL,
    lon         REAL,
    ele_m       REAL,
    PRIMARY KEY (ride_id, seq)
) WITHOUT ROWID;

-- Secrets and tokens in this table are secretbox-sealed (v1. prefix), never plaintext.
CREATE TABLE strava_links (
    rider_id           INTEGER PRIMARY KEY REFERENCES riders(id) ON DELETE CASCADE,
    client_id          TEXT NOT NULL DEFAULT '',
    client_secret_enc  TEXT NOT NULL DEFAULT '',
    athlete_id         INTEGER,
    athlete_name       TEXT NOT NULL DEFAULT '',
    scope              TEXT NOT NULL DEFAULT '',
    access_enc         TEXT NOT NULL DEFAULT '',
    refresh_enc        TEXT NOT NULL DEFAULT '',
    expires_at         TEXT,
    auto_upload        INTEGER NOT NULL DEFAULT 0,
    connected_at       TEXT
);
