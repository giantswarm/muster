# Custom Makefile targets for muster
# This file is included by the main Makefile via `include Makefile.*.mk`

##@ Development

.PHONY: helm-lint
helm-lint: ## Run Helm linter
	@echo "Running Helm linter..."
	@helm lint helm/muster/

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
