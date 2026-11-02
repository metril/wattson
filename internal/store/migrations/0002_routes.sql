ALTER TABLE riders ADD COLUMN lead_s REAL NOT NULL DEFAULT 4;

CREATE TABLE routes (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    name        TEXT NOT NULL,
    source      TEXT NOT NULL CHECK (source IN ('gpx','tcx','fit','draw','synthetic')),
    distance_m  REAL NOT NULL,
    ascent_m    REAL NOT NULL,
    descent_m   REAL NOT NULL,
    min_grade_pct REAL NOT NULL,
    max_grade_pct REAL NOT NULL,
    bbox        TEXT NOT NULL,
    geojson     TEXT NOT NULL,
    spec        TEXT NOT NULL DEFAULT '{}',
    points      BLOB NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX routes_created_at ON routes(created_at);
