# Loops exec tests

Run the live CPU tests against a disposable test workspace:

```sh
export BASETEN_E2E_TEST_API_KEY=<test-workspace-key>
export BASETEN_E2E_TEST_REMOTE_URL=<test-workspace-url>
go test -v -tags=e2e -run '^TestE2ELoopsExec$' -count=1 -timeout 30m ./internal/e2e-tests/...
```

The existing E2E CI step discovers this test. Without the API key it skips;
a skipped run is not live validation. These tests create billable CPU jobs,
use unique project names, and delete their projects during cleanup, including
after assertion failures. Cleanup failures fail the test and require inspection
of the logged project name. No GPU or new API key is requested.

## Coverage

| Behavior | Automated coverage |
| --- | --- |
| Uploaded directory, quoted argument, environment value | Live CPU fixture asserts received values |
| Successful execution | Live terminal status, completion log, and CLI success |
| Failed execution | Live terminal status, traceback marker, and CLI failure |
| JSON separate from logs | Live stdout parses as one job result; markers appear on stderr |
| Delayed final logs and cancellation | Deterministic command tests in `command.loops_test.go` |
| Flag validation and credential forwarding | Command tests in `command.loops_test.go` |

The CPU fixture tests the managed client launcher, not Loops model training.
It uses `--no-api-key` to avoid provisioning remote credentials.

## Validation record

On September 30, 2026, the new CPU success case passed against Baseten.
The first failure-case attempt hit a TCP read timeout while fetching logs;
the test failed and cleaned up its project. That attempt does not validate
remote failure reporting. An unchanged rerun of the failure case passed in
47 seconds, capturing the traceback, CLI error, and `TRAINING_JOB_FAILED`
status. The ordinary `go test ./...` suite, `go vet ./...`, and E2E-tagged
static checks passed. Running without credentials confirmed that it skips.

## Live GPU validation and remaining coverage

On September 30, 2026, commit `56368cf` was tested with Truss 0.18.32 and
Loops SDK 0.24.1. An H100 client executed a checked CUDA kernel and completed
a Qwen3.5-2B forward/backward pass and optimizer step on one B200. Success,
intentional failure, Ctrl-C leaving the job running, and log resumption were
checked. Test jobs and compute were cleaned up. These were manual checks,
not tests run by CI.

The default CUDA image failed with `--with-uv` because it lacked both `pip`
and `curl`. The successful run used
`pytorch/pytorch:2.7.1-cuda12.8-cudnn9-devel`. The upstream Truss bootstrap
issue remains; the CPU test does not cover it.

Before claiming full Loops E2E coverage, add automated GPU training with
guaranteed trainer cleanup, live cancellation and log resumption, OAuth refresh,
and first-use team API-key provisioning. Sampling, checkpoint deployment, and
long-running training are outside this launcher's tests.
