-- +goose Up
CREATE TABLE acquisition_script (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    name VARCHAR(100) NOT NULL,
    description TEXT NOT NULL DEFAULT '',
    draft_source TEXT NOT NULL DEFAULT '',
    published_version_id INTEGER,
    create_by INTEGER,
    update_by INTEGER,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    deleted INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT ck_acquisition_script_name_not_blank CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_script_deleted CHECK (deleted IN (0, 1)),
    FOREIGN KEY (id, published_version_id) REFERENCES acquisition_script_version (script_id, id) ON DELETE RESTRICT
);
CREATE UNIQUE INDEX uk_acquisition_script_name ON acquisition_script (name) WHERE deleted = 0;

CREATE TABLE acquisition_script_version (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    script_id INTEGER NOT NULL REFERENCES acquisition_script (id) ON DELETE RESTRICT,
    version_no INTEGER NOT NULL CHECK (version_no > 0),
    source TEXT NOT NULL,
    checksum VARCHAR(64) NOT NULL CHECK (length(checksum) = 64 AND checksum NOT GLOB '*[^0-9A-Fa-f]*'),
    published_by INTEGER NOT NULL,
    published_at DATETIME NOT NULL,
    UNIQUE (script_id, version_no),
    UNIQUE (script_id, id)
);
ALTER TABLE acquisition_device ADD COLUMN script_id INTEGER REFERENCES acquisition_script (id) ON DELETE RESTRICT;
CREATE INDEX idx_acquisition_device_script ON acquisition_device (script_id);

-- +goose StatementBegin
CREATE TRIGGER acquisition_script_version_no_update BEFORE UPDATE ON acquisition_script_version
BEGIN
    SELECT RAISE(ABORT, 'script versions are immutable');
END;
-- +goose StatementEnd
-- +goose StatementBegin
CREATE TRIGGER acquisition_script_version_no_delete BEFORE DELETE ON acquisition_script_version
BEGIN
    SELECT RAISE(ABORT, 'script versions are immutable');
END;
-- +goose StatementEnd

-- +goose Down
DROP TRIGGER acquisition_script_version_no_update;
DROP TRIGGER acquisition_script_version_no_delete;
DROP INDEX idx_acquisition_device_script;
ALTER TABLE acquisition_device DROP COLUMN script_id;
PRAGMA defer_foreign_keys = ON;
DROP TABLE acquisition_script;
DROP TABLE acquisition_script_version;
