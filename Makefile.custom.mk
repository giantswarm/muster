# Custom Makefile targets for muster
# This file is included by the main Makefile via `include Makefile.*.mk`

##@ Development

.PHONY: helm-lint
helm-lint: ## Run Helm linter
	@echo "Running Helm linter..."
	@helm lint helm/muster/

HELM_UNITTEST_VERSION := 1.0.3
YQ_VERSION := v4.44.6

# What CI runs (the chart-test job in .circleci/custom.yml). Deliberately not
# the full helm-unittest suite: tests/pdb_test.yaml and tests/gateway_api_test.yaml
# fail on main because values.schema.json rejects values those suites set
# (free-form label/annotation maps, a percentage-string minAvailable, and an
# undeclared maxUnavailable). Widen this to helm-test once those are fixed.
.PHONY: helm-test
helm-test: helm-lint helm-alerting-test ## Run the chart checks that pass today (what CI runs).

.PHONY: helm-alerting-test
helm-alerting-test: ## Run the PrometheusRule checks: its helm-unittest suite plus the promtool alert-rule tests.
	@echo "Running PrometheusRule chart tests..."
	@$(MAKE) --no-print-directory helm-plugin-unittest
	@helm unittest helm/muster/ -f 'tests/prometheusrule_test.yaml'
	@$(MAKE) --no-print-directory helm-promtool-test

.PHONY: helm-test-all
helm-test-all: helm-lint helm-unittest helm-promtool-test ## Run every chart check, including the suites that fail on main.

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
# Makefile.gen.go.mk; these prerequisites run before it (CRD freshness, then the
# integration suite) and only add prerequisites -- they do not override the
# generated recipe.
test: verify-crds muster-integration-test

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
	./muster test --parallel 50 --base-port 30000

.PHONY: test-envtest
test-envtest: ## Run the envtest-backed RBAC integration tests (downloads a kube-apiserver via setup-envtest).
	@echo "Running envtest RBAC integration tests..."
	KUBEBUILDER_ASSETS="$$(go run sigs.k8s.io/controller-runtime/tools/setup-envtest@release-0.24 use -p path)" \
		go test ./internal/mcpserver/ ./internal/workflow/ -run TestWritesAsCallerEnvtest -count=1 -v

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
