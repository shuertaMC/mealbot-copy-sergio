CREATE TABLE IF NOT EXISTS organizations (
    name VARCHAR PRIMARY KEY,
    admin VARCHAR NOT NULL CHECK (length(admin) > 0),
    cross_match_trait VARCHAR
);

CREATE TABLE IF NOT EXISTS members (
    organization VARCHAR NOT NULL REFERENCES organizations(name),
    email VARCHAR NOT NULL CHECK (length(email) > 0),
    name VARCHAR NOT NULL CHECK (length(name) > 0),
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    last_round_with JSONB NOT NULL DEFAULT '{}'::jsonb,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    PRIMARY KEY (organization, email)
);

CREATE TABLE IF NOT EXISTS rounds (
    organization VARCHAR NOT NULL REFERENCES organizations(name),
    id INTEGER NOT NULL CHECK (id >= 0),
    scheduled_date TIMESTAMP WITHOUT TIME ZONE NOT NULL,
    done BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (organization, id)
);

CREATE TABLE IF NOT EXISTS pairs (
    organization VARCHAR NOT NULL REFERENCES organizations(name),
    id1 VARCHAR NOT NULL CHECK (length(id1) > 0),
    id2 VARCHAR NOT NULL CHECK (length(id2) > 0),
    extraid VARCHAR,
    round INTEGER NOT NULL CHECK (round >= 0),
    CONSTRAINT pairs_identity_unique
        UNIQUE NULLS NOT DISTINCT (organization, id1, id2, extraid, round),
    FOREIGN KEY (organization, round) REFERENCES rounds(organization, id),
    FOREIGN KEY (organization, id1) REFERENCES members(organization, email),
    FOREIGN KEY (organization, id2) REFERENCES members(organization, email)
);
