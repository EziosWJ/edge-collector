-- +goose Up
CREATE TABLE acquisition_register_block (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    device_id INTEGER NOT NULL,
    name VARCHAR(100) NOT NULL,
    function_code INTEGER NOT NULL,
    start_address INTEGER NOT NULL,
    quantity INTEGER NOT NULL,
    sort_order INTEGER NOT NULL DEFAULT 0,
    create_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    update_time DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    CONSTRAINT fk_acquisition_register_block_device FOREIGN KEY (device_id) REFERENCES acquisition_device (id) ON DELETE CASCADE,
    CONSTRAINT ck_acquisition_register_block_name_not_blank CHECK (length(trim(name)) > 0),
    CONSTRAINT ck_acquisition_register_block_function_code CHECK (function_code IN (3, 4)),
    CONSTRAINT ck_acquisition_register_block_start_address CHECK (start_address BETWEEN 0 AND 65535),
    CONSTRAINT ck_acquisition_register_block_quantity CHECK (quantity BETWEEN 1 AND 125),
    CONSTRAINT ck_acquisition_register_block_end_address CHECK (start_address + quantity - 1 <= 65535),
    CONSTRAINT uk_acquisition_register_block_device_name UNIQUE (device_id, name)
);

CREATE INDEX idx_acquisition_register_block_device_order
    ON acquisition_register_block (device_id, sort_order, id);

-- +goose Down
DROP TABLE IF EXISTS acquisition_register_block;
