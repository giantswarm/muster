# Debugging

How to read a failure the console does not explain: a scenario that fails only in CI, a race
report, and a CLI change that needs a live proof.

## A scenario that fails in CI

`make test` writes the suite's report into `test-reports/` (`muster-test-report-<time>.json`).
When the `go-build` job fails, CircleCI keeps that directory as the job's `test-reports`
artifact. The console shows only the end of the suite's output; the report holds every step's
response and the whole log of every instance.

With a CircleCI token (header `Circle-Token`):

- the artifacts: `https://circleci.com/api/v2/project/gh/giantswarm/muster/<job number>/artifacts`
  lists the report's URL;
- a step's console: `https://circleci.com/api/v1.1/project/github/giantswarm/muster/<job number>`
  has, for each entry of `.steps[]`, an `.actions[0].output_url` whose JSON array holds the
  output in `.[].message`.

In the report, `.scenario_results[]` carries `.scenario.Name` (the scenario's keys are
capitalised), `.result` (`PASSED`, `FAILED`, `ERROR` or `SKIPPED`), `.step_results[]` with
`.step.ID`, `.response`, `.start_time` and `.end_time`, and `.instance_logs.Stderr`, the
instance's whole log with timestamps:

```bash
jq -r '.scenario_results[] | select(.result != "PASSED") | .scenario.Name' muster-test-report-*.json
```

The steps' times show whether the harness was alive while a scenario was silent. A stalled call
leaves goroutine dumps: the instance's in its log, the harness's own in `harness_goroutines`.

CI runs the suite on fewer and slower CPUs than a laptop, so a timing failure seen only there
rarely reproduces in local runs, even pinned to two CPUs. Read the report instead of looping the
suite, and fix the race it shows: a rerun is not a fix.

## Race reports

- Read the top frames of the two conflicting accesses (`Write at …`, `Previous read at …`) and the
  `Goroutine N created at` stack. The previous access's stack can carry stale frames spliced in
  below `cmd.runServe` (a leaf function calling `runServe` cannot happen); they point at code
  that is not involved.
- A startup race reproduces without the suite: `go build -race -o ./muster .`, hold the
  aggregator's port with another listener, and run
  `GORACE=halt_on_error=1 OTEL_METRICS_EXPORTER=prometheus ./muster serve --config-path <dir>`.
  Unsynchronised state on the aggregator's failure path reports within a second.
- Across the suite, run the race build's `./muster test --report <dir>`; the report lands in the
  directory, and `grep -r "DATA RACE" <dir>` finds every report. `halt_on_error=1` stops each
  process at its first race, so leave it off to see them all.
- A test that hangs only under `-race` usually assumes an order of events that the race build's
  timing breaks.

## Proving a CLI change live

- Start a scratch instance of your build on a free port: a configuration directory whose
  `config.yaml` holds `aggregator: {port: <port>, host: localhost}`, with empty `workflows/` and
  `mcpservers/`, served by `./muster serve --config-path <dir>`. `/health` answers
  `{"status":"ok"}` once it is up. Stop it by the PID you recorded when you started it.
- Point every CLI call at it with `--endpoint http://localhost:<port>/mcp --auth none`; without
  them the CLI talks to the current context, which may be a remote muster that wants a sign-in.
- Tools to call: `muster create mcpserver files --type=stdio --command=npx --args="-y,@modelcontextprotocol/server-filesystem,<dir>" --autoStart=true`
  (with the same two flags) brings `x_files_*` into `muster list tools` within seconds. Workflow
  files in `<dir>/workflows/` are loaded when the instance starts.
- What a workflow received is the "Input Parameters" table of `muster start workflow` and
  `muster call workflow_<name>`, and later `.input` of
  `muster get workflow-execution <id> -o json`. The list (`muster list workflow-execution`) carries
  no input, and `--debug` on `muster call` shows the session's handshake, not the call's arguments.
- For a before and after, `git archive HEAD | tar -x -C <dir>` and a `go build` there give the
  binary without your change.
- Argument parsing has no scenario step: cover it with unit tests that run the command line
  through the real cobra command, as `cmd/dynamic_args_test.go` does.
