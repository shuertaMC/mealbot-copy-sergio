#!/usr/bin/env bash
# Checks that every dependency uses an allowed license (same check as CI).
# Requires a Go toolchain; installs go-licenses into $(go env GOPATH)/bin.
# Usage: scripts/check-licenses.sh
set -euo pipefail

cd "$(dirname "$0")/.."

ALLOWED="MIT,BSD-2-Clause,BSD-3-Clause,Apache-2.0,ISC,MPL-2.0"

go mod tidy
go mod download
go install github.com/google/go-licenses@v1.6.0
"$(go env GOPATH)/bin/go-licenses" check ./... --allowed_licenses="$ALLOWED"
