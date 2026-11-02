CREATE TABLE workouts (
    id          INTEGER PRIMARY KEY,
    user_id     INTEGER REFERENCES users(id) ON DELETE SET NULL,
    name        TEXT NOT NULL,
    type        TEXT NOT NULL CHECK (type IN ('interval','erg')),
    blocks_json TEXT NOT NULL,
    duration_s  INTEGER NOT NULL,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX workouts_created_at ON workouts(created_at);

-- Learned per-bike power model (bike.PowerModelState JSON), keyed by bike identity.
CREATE TABLE power_models (
    bike_key   TEXT PRIMARY KEY,
    state      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
