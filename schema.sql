-- Non-destructive psql bootstrap for local development.
-- Production and application startup should call postgres.Repository.Migrate,
-- which executes these same embedded migrations with a transactional ledger.
\set ON_ERROR_STOP on
BEGIN;
\ir migrations/001_initial_schema.sql
\ir migrations/002_reconcile_legacy_schema.sql
\ir migrations/003_constraints_and_indexes.sql
\ir migrations/004_stable_round_claims.sql
COMMIT;
