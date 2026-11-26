-- DESTRUCTIVE: disposable PostgreSQL integration-test databases only.
DROP TABLE IF EXISTS pairs CASCADE;
DROP TABLE IF EXISTS rounds CASCADE;
DROP TABLE IF EXISTS members CASCADE;
DROP TABLE IF EXISTS organizations CASCADE;
DROP TABLE IF EXISTS mealbot_schema_migrations CASCADE;
