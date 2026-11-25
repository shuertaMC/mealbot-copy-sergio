CREATE INDEX IF NOT EXISTS organizations_admin_idx
    ON organizations (admin, name);

CREATE INDEX IF NOT EXISTS members_organization_active_name_idx
    ON members (organization, active, name);

CREATE INDEX IF NOT EXISTS rounds_due_idx
    ON rounds (done, scheduled_date, organization, id);

CREATE INDEX IF NOT EXISTS pairs_organization_round_idx
    ON pairs (organization, round);
