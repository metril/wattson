CREATE TABLE users (
    id            INTEGER PRIMARY KEY,
    username      TEXT NOT NULL UNIQUE COLLATE NOCASE,
    email         TEXT,
    display_name  TEXT NOT NULL DEFAULT '',
    password_hash TEXT,
    iss           TEXT,
    sub           TEXT,
    is_admin      INTEGER NOT NULL DEFAULT 0,
    is_bootstrap  INTEGER NOT NULL DEFAULT 0,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL
);
CREATE UNIQUE INDEX users_iss_sub ON users(iss, sub) WHERE iss IS NOT NULL;
CREATE UNIQUE INDEX users_one_bootstrap ON users(is_bootstrap) WHERE is_bootstrap = 1;

CREATE TABLE sessions (
    id_hash    TEXT PRIMARY KEY,
    user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL
);
CREATE INDEX sessions_expires_at ON sessions(expires_at);

CREATE TABLE api_tokens (
    id           INTEGER PRIMARY KEY,
    user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    prefix       TEXT NOT NULL,
    hash         TEXT NOT NULL UNIQUE,
    scopes       TEXT NOT NULL DEFAULT '[]',
    created_at   TEXT NOT NULL,
    last_used_at TEXT
);
CREATE INDEX api_tokens_user_id ON api_tokens(user_id);

CREATE TABLE oidc_providers (
    id                INTEGER PRIMARY KEY,
    slug              TEXT NOT NULL UNIQUE,
    display_name      TEXT NOT NULL,
    issuer_url        TEXT NOT NULL,
    client_id         TEXT NOT NULL,
    client_secret_enc TEXT NOT NULL DEFAULT '',
    scopes            TEXT NOT NULL DEFAULT 'openid profile email groups',
    groups_claim      TEXT NOT NULL DEFAULT 'groups',
    allowed_groups    TEXT NOT NULL DEFAULT '[]',
    allowed_emails    TEXT NOT NULL DEFAULT '[]',
    admin_group       TEXT NOT NULL DEFAULT '',
    auto_create_users INTEGER NOT NULL DEFAULT 1,
    link_by_email     INTEGER NOT NULL DEFAULT 0,
    enabled           INTEGER NOT NULL DEFAULT 1,
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

CREATE TABLE riders (
    id               INTEGER PRIMARY KEY,
    user_id          INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name             TEXT NOT NULL,
    weight_kg        REAL NOT NULL,
    bike_kg          REAL NOT NULL,
    ftp_w            INTEGER NOT NULL,
    min_level        INTEGER NOT NULL,
    max_level        INTEGER NOT NULL,
    gain             REAL NOT NULL,
    offset_level     REAL NOT NULL,
    downhill_scale   REAL NOT NULL,
    cda              REAL NOT NULL,
    crr              REAL NOT NULL,
    power_multiplier REAL NOT NULL,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL
);
CREATE INDEX riders_user_id ON riders(user_id);

CREATE TABLE settings (
    section    TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
