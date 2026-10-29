DROP TABLE IF EXISTS pairs;
DROP TABLE IF EXISTS rounds;
DROP TABLE IF EXISTS members;
DROP TABLE IF EXISTS organizations;

CREATE TABLE organizations (
    name VARCHAR PRIMARY KEY,
    admin VARCHAR NOT NULL CHECK(length(admin) > 0),
    cross_match_trait VARCHAR
);

CREATE TABLE members (
    organization VARCHAR REFERENCES organizations(name),
    email VARCHAR NOT NULL CHECK(length(email) > 0),
    name VARCHAR NOT NULL CHECK(length(name) > 0),
    metadata JSONB,
    last_round_with JSONB NOT NULL,
    active BOOLEAN NOT NULL,
    PRIMARY KEY (organization, email)
);

CREATE TABLE rounds (
    organization VARCHAR REFERENCES organizations(name),
    id INTEGER NOT NULL CHECK (id >= 0),
    -- round dates are stored in UTC with accuracy up to seconds
    -- when retrieved, it is formatted as a string in 'YYYY-MM-DDTHH:mm:ssZ',
    -- the trailing Z indicating that that the datestring should be parsed 
    -- as UTC time (& converted into local time if a client library wants to
    -- do so e.g moment.js)
    scheduled_date TIMESTAMP NOT NULL,
    done BOOLEAN NOT NULL,
    PRIMARY KEY (organization, id)
);

CREATE TABLE pairs (
    organization VARCHAR REFERENCES organizations(name),
    id1 VARCHAR NOT NULL CHECK(length(id1) > 0),
    id2 VARCHAR NOT NULL CHECK(length(id2) > 0),
    extraId VARCHAR,
    round INTEGER NOT NULL CHECK(round >= 0),
    PRIMARY KEY (organization, id1, id2, extraId, round),
    FOREIGN KEY (organization, round) REFERENCES rounds(organization, id),
    FOREIGN KEY (organization, id1) REFERENCES members(organization, email),
    FOREIGN KEY (organization, id2) REFERENCES members(organization, email)
);
