# Development setup

What you need to build, test and change muster locally.

## Toolchain

- **Go**: the version named in `go.mod` (`go 1.25`, with the toolchain directive selecting the exact release). `go install` and `make build` use it automatically.
- **golangci-lint**, **goimports**: `make lint` and `make imports` install or expect them; the pre-commit hooks run the same checks.
- **pre-commit**: `pre-commit install` once after cloning. The hooks format Go code, run the linters, enforce Conventional Commit messages, regenerate the Helm values schema and the chart README.
- **Helm** with the `unittest` and `schema` plugins, and **promtool**: only for chart work (`make helm-test`).
- **setup-envtest**: only for the Kubernetes-mode tests (`make test-envtest` downloads a `kube-apiserver` on first run).
- **uv**: only for the documentation site (`make docs-serve`).

## Clone and build

```bash
git clone https://github.com/giantswarm/muster.git
cd muster
pre-commit install
make build          # ./muster
go install          # $(go env GOPATH)/bin/muster, what the scenarios and the harness call
```

Run the binary you built:

```bash
./muster serve --config-path ./tmp-config --debug
./muster agent --repl
```

## Tests

| Command | Runs |
|---|---|
| `make test` | Unit tests with the race detector, `verify-crds`, `verify-cli-docs` and the scenario suite |
| `muster test` | Every behavioural scenario against isolated `muster serve` instances |
| `muster test --scenario <name> --verbose --debug` | One scenario, with the instance's log |
| `muster test --concept workflow` | The scenarios of one concept (`workflow`, `mcpserver`, `service`) |
| `muster test --parallel 50 --base-port 30000` | The suite with fifty instances at a time |
| `make test-envtest` | The RBAC integration tests and the Kubernetes-mode scenarios on envtest |
| `make helm-test` | Chart lint, helm-unittest suites and the promtool alert-rule tests |

Scenarios live in `internal/testing/scenarios/*.yaml`; [Testing](testing/README.md) explains
the framework and [Writing scenarios](testing/scenarios.md) the schema. A failing scenario is a
bug in the code, not in the scenario.

Unit tests never sleep and never use timers to hide a race; new code comes with tests, and the
project holds coverage at eighty percent or more for new code.

## Before every commit

```bash
goimports -local github.com/giantswarm/muster/v5 -w . && go fmt ./...
make lint           # golangci-lint with gosec, goconst and govet
make vet
make test
```

The pre-commit hooks run the formatting and lint steps for you; `make test` is yours to run.

## Architecture rules the linters do not catch

- **Packages communicate through `internal/api`.** Each service package registers an adapter (`api_adapter.go`) and consumers retrieve handlers with `api.GetXxx()`. Importing `internal/workflow` or `internal/mcpserver` from another service package is the one pattern reviewers always send back. [ADR-001](../explanation/decisions/001-api-service-locator.md) explains why.
- **Every package has a `doc.go`.** The package comment says what the package is for and how it is reached through the API layer.
- **Files stay under about four hundred lines.** Split a file that grows past it.
- **Errors are wrapped with context:** `fmt.Errorf("connecting to %s: %w", name, err)`.
- **Exit codes** are `0` success, `1` error, `2` authentication required, `3` authentication failed, `125` a newer release exists (`self-update --check` only); commands return the `internal/cli` error types that map to them.

## Generated files

Regenerate, never edit by hand:

| Files | Command |
|---|---|
| `helm/muster/crds/`, `helm/muster-crds/` CRDs | `make generate-crds`; `make verify-crds` fails when stale |
| `docs/reference/cli/` | `make generate-cli-docs`; `make verify-cli-docs` fails when stale |
| `schema.json` | `muster test --generate-schema` |
| `helm/muster/values.schema.json`, `helm/muster/README.md` | pre-commit hooks (`helm schema`, `helm-docs`) |
| `.circleci/config.yml`, `zz_generated.*` workflows, `Makefile.gen.*.mk`, `renovate.json5` | devctl through the organisation's align-files workflow; change the template in devctl, not the copy here |

## Documentation

The documentation is Markdown under `docs/`, published with MkDocs to
[giantswarm.github.io/muster](https://giantswarm.github.io/muster/). `make docs-serve` renders
it locally with live reload; `make docs-build` is the strict build the pull-request check runs,
which fails on a broken link, a page missing from the navigation in `mkdocs.yml` or an unknown
anchor. Command help texts are documentation too: the CLI reference is rendered from them.

## Configuration during development

`muster serve` reads `~/.config/muster` by default. Point it at a scratch directory with
`--config-path` so that your own definitions stay untouched; `./.muster/config.yaml` in the
repository is picked up as a project configuration when present. `--debug` turns on debug
logging, `--json-rpc` on the agent prints every protocol message.

## Pull requests

- One logical change per pull request, with a Conventional Commit title (`feat(aggregator): ...`, `fix(oauth): ...`); the title becomes the changelog entry's context.
- A changelog entry under `Unreleased` in `CHANGELOG.md`, written for the person who runs or uses muster.
- Sign-off under the DCO on every commit (`git commit -s`).
- CI runs the unit tests, the linters, the scenario suite, the chart tests and the security scans; a merge to `main` is released automatically.
