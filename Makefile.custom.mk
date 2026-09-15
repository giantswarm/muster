# Custom Makefile targets for muster
# This file is included by the main Makefile via `include Makefile.*.mk`

##@ Development

.PHONY: helm-lint
helm-lint: ## Run Helm linter
	@echo "Running Helm linter..."
	@helm lint helm/muster/

HELM_UNITTEST_VERSION := 1.0.3
YQ_VERSION := v4.44.6

# What CI runs (the chart-test job in .circleci/custom.yml).
.PHONY: helm-test
helm-test: helm-lint helm-unittest helm-promtool-test ## Run every chart check (what CI runs).

.PHONY: helm-alerting-test
helm-alerting-test: ## Run only the PrometheusRule checks: its helm-unittest suite plus the promtool alert-rule tests.
	@echo "Running PrometheusRule chart tests..."
	@$(MAKE) --no-print-directory helm-plugin-unittest
	@helm unittest helm/muster/ -f 'tests/prometheusrule_test.yaml'
	@$(MAKE) --no-print-directory helm-promtool-test

.PHONY: helm-unittest
helm-unittest: helm-plugin-unittest ## Run all helm-unittest suites in helm/muster/tests/.
	@echo "Running helm unittest..."
	@helm unittest helm/muster/

.PHONY: helm-plugin-unittest
helm-plugin-unittest:
	@helm plugin list | grep -q '^unittest' || helm plugin install https://github.com/helm-unittest/helm-unittest --version $(HELM_UNITTEST_VERSION)

# Separate from helm-unittest: the alert expressions need a PromQL engine to
# say anything, and only promtool has one. helm-unittest can assert that the
# rule renders; only this can assert that it fires when a backend breaks and
# stays quiet when one is merely waiting for auth.
.PHONY: helm-promtool-test
helm-promtool-test: ## Run the promtool unit tests for the PrometheusRule (requires promtool and mikefarah/yq).
	@echo "Running promtool alert rule tests..."
	@bash helm/muster/tests/promtool/run.sh

##@ Testing

# The architect go-build job runs `make test` (test_target: test). Extend that
# target with the checks that used to live in the hand-written ci.yaml so CI and
# local runs share one command. The `go test` recipe itself lives in
# Makefile.gen.go.mk; these prerequisites run before it (the version stamp for
# the CI binaries, CRD freshness, then the integration suite) and only add
# prerequisites -- they do not override the generated recipe.
test: stamp-version verify-crds verify-cli-docs muster-integration-test

# The version stamped into the binaries CI links: the tag on a tag build,
# otherwise what `git describe` says about HEAD (v5.23.2-1-g4be8379e on a
# branch, with -dirty for uncommitted changes).
STAMP_VERSION ?= $(or $(CIRCLE_TAG),$(shell git describe --tags --always --dirty --match 'v*'))

# The architect orb's go-build job links the binaries with the flags in
# .ldflags, a file its go-test command writes (commit SHA and build time, no
# version) right before it runs `make test`. Without a version ldflag the
# binary falls back to what Go's buildvcs stamped from the checkout: the tag
# on a tag build now that the module path is github.com/giantswarm/muster/v5
# (while it had no /v5 suffix Go only considered v0 and v1 tags, the v5.22.0
# release binary reported v1.12.1-0.20260915144925-e6c760a32b48, and
# `muster self-update` found every release "newer" than itself), but only a
# pseudo-version on a branch and nothing from a checkout without tags.
# `make test` is the one repo-owned step between the orb writing .ldflags and
# linking with it, so this prerequisite appends the version there and every
# CI binary carries one whatever the checkout looks like. Without a .ldflags
# file (a local `make build`, whose generated LDFLAGS already carry the
# version) it does nothing.
.PHONY: stamp-version
stamp-version: ## Append the version to the link flags in .ldflags, the file the architect go-build job links with.
	@if [ -f .ldflags ] && ! grep -q 'pkg/project.version=' .ldflags; then \
		v="$(STAMP_VERSION)"; \
		printf " -X '%s/pkg/project.version=%s'" "$(MODULE)" "$$v" >> .ldflags; \
		echo "Stamped version $$v into .ldflags"; \
	fi

CONTROLLER_GEN_VERSION := v0.21.0

.PHONY: verify-crds
verify-crds: ## Regenerate CRDs and fail if the committed copies are stale.
	@echo "Verifying CRDs are up to date..."
	go install sigs.k8s.io/controller-tools/cmd/controller-gen@$(CONTROLLER_GEN_VERSION)
	PATH="$(shell go env GOPATH)/bin:$$PATH" $(MAKE) generate-crds
	@git diff --exit-code $(CRD_DIR) $(APP_CRD_DIR) || { \
		echo "ERROR: CRDs are out of date. Run 'make generate-crds' and commit."; \
		exit 1; }
	@echo "CRDs are up to date."

.PHONY: muster-integration-test
muster-integration-test: build ## Run the muster integration suite (./muster test).
	@echo "Running muster integration suite..."
	./muster test --parallel 50 --base-port 30000 --report test-reports

.PHONY: test-envtest
test-envtest: build ## Run the envtest-backed tests: the RBAC integration tests and the Kubernetes-mode scenarios (downloads a kube-apiserver via setup-envtest).
	@echo "Running envtest-backed tests..."
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24 use -p path)" \
		go test ./internal/mcpserver/ ./internal/workflow/ ./internal/testing/ -run Envtest -count=1 -v
	@echo "Running the Kubernetes-mode scenarios (mode: kubernetes) against envtest..."
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24 use -p path)" \
		./muster test --mode kubernetes --parallel 8 --base-port 31000 --readiness-timeout 60s --report test-reports-envtest

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

##@ Documentation

DOCS_REQUIREMENTS := requirements-docs.txt

docs-build: ## Build the documentation site into site/; a broken link or a page missing from the nav fails the build.
	uvx --with-requirements $(DOCS_REQUIREMENTS) mkdocs build --strict

docs-serve: ## Serve the documentation site on http://127.0.0.1:8000 with live reload.
	uvx --with-requirements $(DOCS_REQUIREMENTS) mkdocs serve

generate-cli-docs: ## Render docs/reference/cli from the Cobra command tree.
	go run ./hack/gen-cli-docs

verify-cli-docs: generate-cli-docs ## Fail if the committed CLI reference is stale.
	@git diff --exit-code docs/reference/cli || { \
		echo "ERROR: docs/reference/cli is out of date. Run 'make generate-cli-docs' and commit."; \
		exit 1; }
	@echo "CLI reference is up to date."
