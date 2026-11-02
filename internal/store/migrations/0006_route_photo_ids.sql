-- Image ids present in each route's cached photo list, so the image proxy can
-- refuse ids that no route lists. Lists cached before this table are dropped
-- and rebuild on next use.
CREATE TABLE route_photo_ids (
    image_id TEXT NOT NULL,
    route_id INTEGER NOT NULL REFERENCES routes(id) ON DELETE CASCADE,
    PRIMARY KEY (image_id, route_id)
) WITHOUT ROWID;

CREATE INDEX route_photo_ids_route ON route_photo_ids(route_id);

DELETE FROM route_photos;
