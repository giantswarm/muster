# Custom Makefile targets for muster
# This file is included by the main Makefile via `include Makefile.*.mk`

##@ Release

.PHONY: release-dry-run
release-dry-run: ## Test the release process without publishing (all platforms)
	goreleaser release --snapshot --clean --skip=announce,publish,validate

.PHONY: release-dry-run-fast
release-dry-run-fast: ## Fast release dry-run for CI (linux/amd64 only, ~6min faster)
	goreleaser release --config .goreleaser.ci.yaml --snapshot --clean --skip=announce,publish,validate

.PHONY: release-local
release-local: ## Create a release locally
	goreleaser release --clean

##@ Development

.PHONY: lint-yaml
lint-yaml: ## Run YAML linter
	@echo "Running YAML linter..."
	@# Exclude zz_generated files
	@yamllint .github/workflows/auto-release.yaml .github/workflows/ci.yaml .goreleaser.yaml .goreleaser.ci.yaml

.PHONY: helm-lint
helm-lint: ## Run Helm linter
	@echo "Running Helm linter..."
	@helm lint helm/muster/

.PHONY: helm-test
helm-test: ## Run Helm unit tests (requires helm-unittest plugin)
	@echo "Running Helm unit tests..."
	@helm unittest helm/muster/

.PHONY: check
check: lint-yaml helm-lint ## Run YAML and Helm linters

##@ Testing

.PHONY: test-vet
test-vet: ## Run go test and go vet
	@echo "Running Go tests (with NO_COLOR=true)..."
	@NO_COLOR=true go test -cover ./...
	@echo "Running go vet..."
	@go vet ./...

.PHONY: govulncheck
govulncheck: ## Run govulncheck to scan for known vulnerabilities
	@echo "Checking for known vulnerabilities..."
	@command -v govulncheck >/dev/null 2>&1 || { echo "Installing govulncheck..."; go install golang.org/x/vuln/cmd/govulncheck@latest; }
	@govulncheck ./...

# Note: These targets require Docker and 'act' to be installed.
# See: https://github.com/nektos/act#installation

.PHONY: test-ci-pr
test-ci-pr: ## Run 'act' to simulate CI checks for a pull request
	@echo "Simulating CI workflow (pull_request event)..."
	@act pull_request --job check

.PHONY: test-ci-push
test-ci-push: ## Run 'act' to simulate CI checks for a push to main
	@echo "Simulating CI workflow (push event)..."
	@act push --job check

.PHONY: test-auto-release
test-auto-release: ## Run 'act' to simulate the auto-release workflow
	@echo "Simulating Auto-Release workflow (merged pull_request event)..."
	@echo "NOTE: Requires 'merged_pr_event.json' in the project root."
	@echo "NOTE: Git push steps within the workflow are expected to fail locally."
	@act pull_request --job auto_release --eventpath merged_pr_event.json
