# Pre-refactor test baseline

This is the durable record of the lifecycle baseline captured before Mealbot behavior moved from the legacy root package into the modular implementation. It is historical evidence, not a statement about the current test suite.

## Checkpoint and environment

| Field | Captured value |
| --- | --- |
| Lifecycle run | `mealbot-remediate-gates` |
| Git checkpoint | `48472ef703a1b645cfa00fa77b92263275dbcfa2` (`scaffold modular Go service layout`) |
| Start time | `2026-11-25T20:22:34.142065Z` |
| Toolchain | `go version go1.26.11 linux/amd64` |
| Result | Passed, exit code 0, in 7.80 seconds; stderr was empty |

Commit `48472ef` added only the target package skeleton and entry-point scaffold on top of the legacy service. The baseline ran after that structure-only checkpoint and before the subsequent dependency correction and domain/configuration behavior work, so the passing root-package result characterizes the pre-refactor behavior while the new packages still contained no tests.

The lifecycle used its local Go distribution at `$MCODE_DIR/lifecycle/target/toolchains/go`. The toolchain version is recorded by the immediately preceding install evidence in `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-install_install_result.json`.

## Command and result

The baseline gate was the uncached form of `go test ./...`, using the lifecycle's then-current module overlay:

```sh
GOFLAGS="-mod=mod -modfile=$MCODE_DIR/lifecycle/target/mealbot.mod" \
  "$MCODE_DIR/lifecycle/target/toolchains/go/bin/go" test -count=1 ./...
```

The legacy root package `github.com/johnamadeo/mealbot` passed in 0.019 seconds. The eight scaffold packages below were discovered and reported `[no test files]`:

- `github.com/johnamadeo/mealbot/cmd/mealbot`
- `github.com/johnamadeo/mealbot/internal/app`
- `github.com/johnamadeo/mealbot/internal/auth`
- `github.com/johnamadeo/mealbot/internal/domain`
- `github.com/johnamadeo/mealbot/internal/httpapi`
- `github.com/johnamadeo/mealbot/internal/mailer`
- `github.com/johnamadeo/mealbot/internal/pairing`
- `github.com/johnamadeo/mealbot/internal/store/postgres`

## Lifecycle evidence

The authoritative raw artifacts are retained outside the repository in the managed lifecycle directory:

- Structured result: `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-gates_baseline_test_result.json`
- Standard output: `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-gates_baseline_test_stdout.log`
- Empty standard error: `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-gates_baseline_test_stderr.log`
- Full run summary: `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-gates_summary.json`
- Go version and dependency preparation: `$MCODE_DIR/lifecycle/target/runs/mealbot-remediate-install_install_result.json`

Later `baseline_test` artifacts exercise the refactored package graph and are useful regression evidence, but they do not replace this pre-refactor checkpoint.
