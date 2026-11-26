-- Legacy installations used pair_counts while runtime code used
-- last_round_with. Merge both objects (last_round_with wins on duplicate keys)
-- before removing the obsolete column.
ALTER TABLE members ADD COLUMN IF NOT EXISTS last_round_with jsonb;

DO $migration$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'members'
          AND column_name = 'pair_counts'
    ) THEN
        EXECUTE 'UPDATE members
                 SET last_round_with = COALESCE(pair_counts, ''{}''::jsonb)
                                       || COALESCE(last_round_with, ''{}''::jsonb)';
        EXECUTE 'ALTER TABLE members DROP COLUMN pair_counts';
    END IF;
END
$migration$;

UPDATE members SET metadata = '{}'::jsonb WHERE metadata IS NULL;
UPDATE members SET last_round_with = '{}'::jsonb WHERE last_round_with IS NULL;
ALTER TABLE members ALTER COLUMN metadata SET DEFAULT '{}'::jsonb;
ALTER TABLE members ALTER COLUMN metadata SET NOT NULL;
ALTER TABLE members ALTER COLUMN last_round_with SET DEFAULT '{}'::jsonb;
ALTER TABLE members ALTER COLUMN last_round_with SET NOT NULL;

ALTER TABLE rounds ADD COLUMN IF NOT EXISTS processing_started_at timestamptz NULL;

DO $migration$
BEGIN
    IF EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'pairs'
          AND column_name = 'extraid'
    ) AND NOT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'pairs'
          AND column_name = 'extra_id'
    ) THEN
        EXECUTE 'ALTER TABLE pairs RENAME COLUMN extraid TO extra_id';
    END IF;
END
$migration$;
