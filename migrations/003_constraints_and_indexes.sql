-- Rebuild pair constraints so legacy databases gain the third-member foreign
-- key and round ID updates cascade during transactional compaction.
ALTER TABLE members DROP CONSTRAINT IF EXISTS members_organization_fkey;
ALTER TABLE rounds DROP CONSTRAINT IF EXISTS rounds_organization_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_organization_round_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_organization_id1_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_organization_id2_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_round_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_id1_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_id2_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_extra_id_fkey;
ALTER TABLE pairs DROP CONSTRAINT IF EXISTS pairs_pkey;
UPDATE pairs SET extra_id = NULL WHERE extra_id = '';
ALTER TABLE pairs ALTER COLUMN extra_id DROP NOT NULL;
ALTER TABLE pairs ADD CONSTRAINT pairs_pkey PRIMARY KEY (organization, round, id1, id2);

ALTER TABLE members ADD CONSTRAINT members_organization_fkey
    FOREIGN KEY (organization) REFERENCES organizations(name)
    ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE rounds ADD CONSTRAINT rounds_organization_fkey
    FOREIGN KEY (organization) REFERENCES organizations(name)
    ON UPDATE CASCADE ON DELETE CASCADE;

ALTER TABLE pairs ADD CONSTRAINT pairs_round_fkey
    FOREIGN KEY (organization, round) REFERENCES rounds(organization, id)
    ON UPDATE CASCADE ON DELETE CASCADE;
ALTER TABLE pairs ADD CONSTRAINT pairs_id1_fkey
    FOREIGN KEY (organization, id1) REFERENCES members(organization, email)
    ON UPDATE CASCADE ON DELETE RESTRICT;
ALTER TABLE pairs ADD CONSTRAINT pairs_id2_fkey
    FOREIGN KEY (organization, id2) REFERENCES members(organization, email)
    ON UPDATE CASCADE ON DELETE RESTRICT;
ALTER TABLE pairs ADD CONSTRAINT pairs_extra_id_fkey
    FOREIGN KEY (organization, extra_id) REFERENCES members(organization, email)
    ON UPDATE CASCADE ON DELETE RESTRICT;

DO $migration$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'pairs_canonical_order_check') THEN
        ALTER TABLE pairs ADD CONSTRAINT pairs_canonical_order_check
            CHECK (id1 < id2 AND (extra_id IS NULL OR id2 < extra_id));
    END IF;
END
$migration$;

CREATE INDEX IF NOT EXISTS organizations_admin_idx ON organizations(admin, name);
CREATE INDEX IF NOT EXISTS members_organization_active_name_idx ON members(organization, active, name, email);
CREATE INDEX IF NOT EXISTS rounds_due_idx ON rounds(done, scheduled_date, processing_started_at) WHERE done = false;
CREATE INDEX IF NOT EXISTS pairs_organization_round_idx ON pairs(organization, round, id1, id2);
