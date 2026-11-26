ALTER TABLE rounds ADD COLUMN IF NOT EXISTS processing_token text NULL;

-- Legacy claims predate owner tokens. Clear only claims that cannot satisfy
-- the paired timestamp/token invariant; already-valid tokenized claims survive
-- idempotent re-execution.
UPDATE rounds
SET processing_started_at = NULL,
    processing_token = NULL
WHERE (processing_started_at IS NULL) <> (processing_token IS NULL)
   OR length(btrim(processing_token)) = 0;

DO $migration$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'rounds_processing_claim_consistent'
          AND conrelid = 'rounds'::regclass
    ) THEN
        ALTER TABLE rounds ADD CONSTRAINT rounds_processing_claim_consistent
            CHECK (
                (processing_started_at IS NULL AND processing_token IS NULL)
                OR
                (processing_started_at IS NOT NULL
                 AND processing_token IS NOT NULL
                 AND length(btrim(processing_token)) > 0)
            );
    END IF;
END
$migration$;
