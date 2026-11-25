DO $migration$
DECLARE
    has_pair_counts BOOLEAN;
    has_last_round_with BOOLEAN;
BEGIN
    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'members'
          AND column_name = 'pair_counts'
    ) INTO has_pair_counts;

    SELECT EXISTS (
        SELECT 1 FROM information_schema.columns
        WHERE table_schema = current_schema()
          AND table_name = 'members'
          AND column_name = 'last_round_with'
    ) INTO has_last_round_with;

    IF has_pair_counts AND NOT has_last_round_with THEN
        ALTER TABLE members RENAME COLUMN pair_counts TO last_round_with;
    ELSIF has_pair_counts AND has_last_round_with THEN
        UPDATE members
        SET last_round_with = COALESCE(pair_counts, '{}'::jsonb)
                            || COALESCE(last_round_with, '{}'::jsonb);
        ALTER TABLE members DROP COLUMN pair_counts;
    ELSIF NOT has_last_round_with THEN
        ALTER TABLE members
            ADD COLUMN last_round_with JSONB NOT NULL DEFAULT '{}'::jsonb;
    END IF;

    UPDATE members
    SET last_round_with = '{}'::jsonb
    WHERE last_round_with IS NULL;

    ALTER TABLE members
        ALTER COLUMN last_round_with SET DEFAULT '{}'::jsonb,
        ALTER COLUMN last_round_with SET NOT NULL;
END;
$migration$;
