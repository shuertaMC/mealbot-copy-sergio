DO $migration$
DECLARE
    legacy_primary_key NAME;
BEGIN
    SELECT candidate.conname
    INTO legacy_primary_key
    FROM pg_constraint AS candidate
    INNER JOIN pg_class AS relation
        ON relation.oid = candidate.conrelid
    INNER JOIN pg_namespace AS namespace
        ON namespace.oid = relation.relnamespace
    WHERE namespace.nspname = current_schema()
      AND relation.relname = 'pairs'
      AND candidate.contype = 'p'
      AND EXISTS (
          SELECT 1
          FROM unnest(candidate.conkey) AS key_column(attnum)
          INNER JOIN pg_attribute AS attribute
              ON attribute.attrelid = candidate.conrelid
             AND attribute.attnum = key_column.attnum
          WHERE attribute.attname = 'extraid'
      )
    LIMIT 1;

    IF legacy_primary_key IS NOT NULL THEN
        EXECUTE format(
            'ALTER TABLE %I.%I DROP CONSTRAINT %I',
            current_schema(),
            'pairs',
            legacy_primary_key
        );
    END IF;

    ALTER TABLE pairs ALTER COLUMN extraid DROP NOT NULL;

    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint AS candidate
        INNER JOIN pg_class AS relation
            ON relation.oid = candidate.conrelid
        INNER JOIN pg_namespace AS namespace
            ON namespace.oid = relation.relnamespace
        WHERE namespace.nspname = current_schema()
          AND relation.relname = 'pairs'
          AND candidate.conname = 'pairs_identity_unique'
    ) THEN
        ALTER TABLE pairs
            ADD CONSTRAINT pairs_identity_unique
            UNIQUE NULLS NOT DISTINCT (organization, id1, id2, extraid, round);
    END IF;
END;
$migration$;
