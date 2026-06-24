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

# TEMPORARY DIAGNOSTIC -- remove once the +dirty culprit is found.
# Release binaries embed `vX.Y.Z+dirty` because Go's build-info VCS stamp records
# `vcs.modified=true` at link time, i.e. `git status` is non-empty when architect's
# go-build links the binaries. A first run showed the tree clean at the end of the
# `test` PREREQUISITES -- but that point is before the generated `go test ./...`
# recipe, so a file left by `go test` itself wouldn't show. This wrapper runs the
# full `test` (incl. the go test recipe) and only THEN the diagnostic, the last
# consumer-controlled point before architect's nancy + Build binaries steps.
# .circleci/workflows.yml points test_target at this wrapper for the diagnostic.
.PHONY: test-diag
test-diag: test vcs-dirty-diagnostic

.PHONY: vcs-dirty-diagnostic
vcs-dirty-diagnostic:
	@echo "VCSDIRTY>> ===== git status --porcelain (untracked=all; what go buildvcs reads) ====="
	@git status --porcelain --untracked-files=all || true
	@echo "VCSDIRTY>> ===== tracked diffs (name + stat) ====="
	@git --no-pager diff --stat || true
	@echo "VCSDIRTY>> ===== ignored artifacts currently present (context only) ====="
	@git status --porcelain --ignored=matching --untracked-files=all 2>/dev/null | grep '^!!' | head -30 || true
	@echo "VCSDIRTY>> ===== end ====="

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
