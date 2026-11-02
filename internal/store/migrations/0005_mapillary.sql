-- Street photos selected along a route (mapillary.Photo list, gzip JSON). The
-- token fingerprint says which Mapillary token built them; a changed token
-- rebuilds. status is ready or error.
CREATE TABLE route_photos (
    route_id INTEGER PRIMARY KEY REFERENCES routes(id) ON DELETE CASCADE,
    status   TEXT NOT NULL,
    token_fp TEXT NOT NULL,
    built_at INTEGER NOT NULL,
    error    TEXT NOT NULL DEFAULT '',
    photos   BLOB
);
