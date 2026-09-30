# Loops exec tests

Run the live CPU tests against a disposable test workspace:

```sh
export BASETEN_E2E_TEST_API_KEY=<test-workspace-key>
export BASETEN_E2E_TEST_REMOTE_URL=<test-workspace-url>
go test -v -tags=e2e -run '^TestE2ELoopsExec$' -count=1 -timeout 25m ./internal/e2e-tests/...
```

CI runs this test in its own Ubuntu step. Fork PRs skip the step; trusted runs
fail if E2E credentials are missing. Local runs without the API key skip.
These tests create billable CPU jobs in one uniquely named project and delete
that project and its cache during cleanup, including after assertion failures.
Cleanup uses a separate timeout and confirms that the project is absent.
Cleanup failures fail the test and require inspection of the logged project
name. No GPU or new API key is requested.

## Coverage

- The remote fixture checks the uploaded directory, a quoted argument, and an environment value containing spaces.
- Success requires a completion log, CLI exit 0, and backend status `COMPLETED`.
- Failure requires the final traceback, CLI exit 1, and backend status `FAILED`.
- Stdout must contain one job JSON result, with remote logs on stderr.
- Cancelling the command after a readiness marker must return exit 130 and leave the remote job running. Resuming logs must receive a newer heartbeat, then explicit stop must reach `STOPPED`.
- Command tests in `command.loops_test.go` cover the delayed-log race, flag validation, and credential forwarding.

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

Automated GPU training with guaranteed trainer cleanup, OAuth refresh,
and first-use team API-key provisioning remain outside the CPU suite. Sampling, checkpoint deployment, and
long-running training are outside this launcher's tests.
