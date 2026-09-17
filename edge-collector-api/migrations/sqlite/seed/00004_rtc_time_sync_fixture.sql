-- +goose Up
-- 开发 RTC 模拟夹具由 SQLite dev migration 单独管理，避免污染 test/prod seed。

-- +goose Down
-- Intentionally empty: the dev-only migration owns this fixture.
