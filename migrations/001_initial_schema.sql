CREATE TABLE IF NOT EXISTS organizations (
    name text PRIMARY KEY,
    admin text NOT NULL CHECK (length(btrim(admin)) > 0),
    cross_match_trait text NULL CHECK (cross_match_trait IS NULL OR length(btrim(cross_match_trait)) > 0)
);

CREATE TABLE IF NOT EXISTS members (
    organization text NOT NULL,
    email text NOT NULL CHECK (length(btrim(email)) > 0),
    name text NOT NULL CHECK (length(btrim(name)) > 0),
    metadata jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(metadata) = 'object'),
    last_round_with jsonb NOT NULL DEFAULT '{}'::jsonb CHECK (jsonb_typeof(last_round_with) = 'object'),
    active boolean NOT NULL DEFAULT true,
    PRIMARY KEY (organization, email),
    CONSTRAINT members_organization_fkey FOREIGN KEY (organization)
        REFERENCES organizations(name) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS rounds (
    organization text NOT NULL,
    id integer NOT NULL CHECK (id >= 0),
    scheduled_date timestamptz NOT NULL,
    done boolean NOT NULL DEFAULT false,
    processing_started_at timestamptz NULL,
    PRIMARY KEY (organization, id),
    CONSTRAINT rounds_organization_fkey FOREIGN KEY (organization)
        REFERENCES organizations(name) ON UPDATE CASCADE ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS pairs (
    organization text NOT NULL,
    id1 text NOT NULL CHECK (length(btrim(id1)) > 0),
    id2 text NOT NULL CHECK (length(btrim(id2)) > 0),
    extra_id text NULL CHECK (extra_id IS NULL OR length(btrim(extra_id)) > 0),
    round integer NOT NULL CHECK (round >= 0),
    PRIMARY KEY (organization, round, id1, id2),
    CONSTRAINT pairs_canonical_order_check CHECK (id1 < id2 AND (extra_id IS NULL OR id2 < extra_id)),
    CONSTRAINT pairs_round_fkey FOREIGN KEY (organization, round)
        REFERENCES rounds(organization, id) ON UPDATE CASCADE ON DELETE CASCADE,
    CONSTRAINT pairs_id1_fkey FOREIGN KEY (organization, id1)
        REFERENCES members(organization, email) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT pairs_id2_fkey FOREIGN KEY (organization, id2)
        REFERENCES members(organization, email) ON UPDATE CASCADE ON DELETE RESTRICT,
    CONSTRAINT pairs_extra_id_fkey FOREIGN KEY (organization, extra_id)
        REFERENCES members(organization, email) ON UPDATE CASCADE ON DELETE RESTRICT
);

CREATE INDEX IF NOT EXISTS organizations_admin_idx ON organizations(admin, name);
CREATE INDEX IF NOT EXISTS members_organization_active_name_idx ON members(organization, active, name, email);
CREATE INDEX IF NOT EXISTS pairs_organization_round_idx ON pairs(organization, round, id1, id2);
