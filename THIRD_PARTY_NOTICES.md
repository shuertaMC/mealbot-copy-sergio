# Third-party notices

Mealbot's own license remains in `LICENSE`. This file records the permissive
licenses of the target runtime, database, direct Go dependencies, their
retained runtime transitive dependencies, and CI security tooling. Versions
are those selected for the Go 1.27.4/PostgreSQL 19 delivery; `go.mod` and
`go.sum` are authoritative for resolved Go module versions.

## Dependency inventory

| Component | Version | Use | License |
| --- | --- | --- | --- |
| Go toolchain and standard library | 1.27.4 | Build and runtime | BSD 3-Clause |
| PostgreSQL | 19.0 | Operated database | PostgreSQL License |
| `github.com/jackc/pgx/v5` | 5.13.0 | PostgreSQL `database/sql` driver | MIT |
| `github.com/jackc/pgpassfile` | 1.0.0 | pgx runtime dependency | MIT |
| `github.com/jackc/pgservicefile` | `0.0.0-20240606120523-5a60cdf6a761` | pgx runtime dependency | MIT |
| `github.com/jackc/puddle/v2` | 2.2.3 | pgx runtime dependency | MIT |
| `golang.org/x/sync` | 0.22.0 | pgx runtime dependency | BSD 3-Clause |
| `golang.org/x/text` | 0.41.0 | pgx runtime dependency | BSD 3-Clause |
| `github.com/golang-jwt/jwt/v5` | 5.3.1 | JWT validation | MIT |
| `github.com/mailgun/mailgun-go/v5` | 5.20.0 | Mail delivery client | BSD 3-Clause |
| `github.com/google/uuid` | 1.6.0 | Mailgun client runtime dependency | BSD 3-Clause |
| `github.com/oapi-codegen/runtime` | 1.8.0 | Mailgun client runtime dependency | Apache License 2.0 |
| `golang.org/x/vuln` (`govulncheck`) | 1.10.0 | CI-only vulnerability scanner | BSD 3-Clause |

Test-only modules are not shipped as part of the service. The obsolete
`vendor/` and `Godeps/` snapshots were removed after the clean module-cache
download, verification, test, and build proof succeeded. Their former contents
remain recoverable from Git history and are not part of the runtime dependency
set above.

## Copyright and license references

- Go and `golang.org/x/*`: Copyright The Go Authors, BSD 3-Clause License,
  <https://go.dev/LICENSE>.
- PostgreSQL: Copyright 1996-2026 The PostgreSQL Global Development Group and
  Copyright 1994 The Regents of the University of California, PostgreSQL
  License, <https://www.postgresql.org/about/licence/>.
- pgx, pgpassfile, pgservicefile, and puddle: Copyright Jack Christensen and
  contributors, MIT License, <https://github.com/jackc/pgx/blob/v5.13.0/LICENSE>.
- golang-jwt/jwt: Copyright Dave Grijalva and the golang-jwt maintainers, MIT
  License, <https://github.com/golang-jwt/jwt/blob/v5.3.1/LICENSE>.
- mailgun-go: Copyright Michael Banzon and contributors, BSD 3-Clause License,
  <https://github.com/mailgun/mailgun-go/blob/v5.20.0/LICENSE>.
- google/uuid: Copyright Google Inc., BSD 3-Clause License,
  <https://github.com/google/uuid/blob/v1.6.0/LICENSE>.
- oapi-codegen/runtime: Copyright the oapi-codegen contributors, Apache License
  2.0, <https://github.com/oapi-codegen/runtime/blob/v1.8.0/LICENSE>.

The MIT, BSD 3-Clause, Apache 2.0, and PostgreSQL licenses permit commercial
use, modification, and redistribution subject to their respective notice,
attribution, disclaimer, and other stated conditions. Preserve the upstream
license text when redistributing source, and reproduce notices required for a
binary distribution. This inventory does not replace the license files in the
module source.

## Hosted services

Auth0 and Mailgun are hosted commercial services configured by operators.
Their service plans, acceptable-use policies, data-processing terms, and other
contract terms are separate from the open-source licenses of the Go JWT and
Mailgun client libraries listed above. This notice does not create or infer an
open-source license obligation for either hosted service.
