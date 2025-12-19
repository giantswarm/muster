# Custom Makefile targets for muster
# This file is included by the main Makefile via `include Makefile.*.mk`

##@ Release

.PHONY: release-dry-run
release-dry-run: ## Run GoReleaser in dry-run mode to validate the release configuration.
	@echo "====> $@"
	goreleaser release --snapshot --clean --skip=publish

