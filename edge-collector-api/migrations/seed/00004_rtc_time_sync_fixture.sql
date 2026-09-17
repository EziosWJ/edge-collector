-- +goose Up
-- RTC 校时模拟夹具只属于开发 SQLite profile；PostgreSQL 保持不写入本地模拟设备。

-- +goose Down
-- Intentionally empty: the matching SQLite-only seed owns this fixture.
