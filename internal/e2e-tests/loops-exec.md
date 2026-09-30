# Loops exec tests

Run the live CPU tests against a disposable test workspace:

The workspace must have Training SSH enabled. Truss requests an on-demand SSH
session for `loops exec`, even when the test does not connect over SSH. If job
creation reports that SSH interactive sessions are disabled, ask the workspace
owner to enable `ORG_ENABLE_TRAINING_SSH` before rerunning the test.

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

## Coverage gaps

GPU training, default GPU image setup with `--with-uv`, OAuth refresh, and
first-use team API-key provisioning still need automated coverage. Sampling,
checkpoint deployment, and long-running training are outside this launcher's
tests.
