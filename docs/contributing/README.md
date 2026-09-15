# Contributing

muster is developed in the open at [github.com/giantswarm/muster](https://github.com/giantswarm/muster).
Bug reports, documentation fixes and features are welcome as issues and pull requests.

## Before you start

- Look for an existing [issue](https://github.com/giantswarm/muster/issues) or open one. For anything larger than a fix, agree on the approach in the issue first.
- Read [Development setup](development-setup.md) for the toolchain, the make targets and the checks that run before every commit.
- Read [Architecture](../explanation/architecture.md), in particular the service locator rule: packages talk to each other only through `internal/api`.

## What a change includes

- **Behaviour is specified by scenarios.** `muster test` runs YAML scenarios against isolated muster instances; a change in behaviour comes with a scenario that shows it, and a failing scenario is fixed in the code, not in the scenario. [Testing](testing/README.md) explains the framework.
- **Unit tests** for new code, without `time.Sleep` and without timers that paper over races.
- **Documentation** in `docs/` when a user-visible surface changes: a tool argument, a configuration key, a CRD field, a CLI flag. The CLI reference is generated from the command tree (`make generate-cli-docs`); everything else is written by hand. `make docs-build` must pass.
- **A changelog entry** under `Unreleased` in `CHANGELOG.md`, written for the person who operates or uses muster: what changed for them and why.

## Conventions

- Commit messages follow [Conventional Commits](https://www.conventionalcommits.org/) (`feat(aggregator): ...`, `fix(oauth): ...`); the pre-commit hook enforces it.
- Every commit is signed off under the [Developer Certificate of Origin](https://github.com/giantswarm/muster/blob/main/DCO) (`git commit -s`).
- Formatting and linting are `gofmt`, `goimports -local github.com/giantswarm/muster` and `golangci-lint` with `gosec` and `goconst`; `make lint` runs them.
- Generated files are regenerated, never edited: CRDs (`make generate-crds`), the CLI reference (`make generate-cli-docs`), the Helm values schema and chart README (pre-commit hooks), `schema.json` (`muster test --generate-schema`).

## Releases

Every pull request merged to `main` produces a release: the version is bumped, the tag is
pushed, and CircleCI builds the multi-architecture image, the signed binaries and the Helm
chart from that tag. There is no manual release step.

## Security

Vulnerabilities are reported through Giant Swarm's
[responsible disclosure process](https://www.giantswarm.io/responsible-disclosure), not as
public issues.
