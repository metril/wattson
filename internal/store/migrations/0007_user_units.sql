ALTER TABLE users ADD COLUMN units TEXT NOT NULL DEFAULT 'metric' CHECK (units IN ('metric','imperial'));
