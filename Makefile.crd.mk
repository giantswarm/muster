# CRD Generation and Sync Targets
#
# This Makefile extension provides targets to generate CRDs from Go types
# and keep deploy/crds and helm/muster/crds in sync.
#
# Source of truth: deploy/crds/ (generated from pkg/apis)
# Copy to: helm/muster/crds/

# CRD directories
CRD_SOURCE_DIR := deploy/crds
CRD_HELM_DIR := helm/muster/crds

# CRD files
CRD_FILES := muster.giantswarm.io_mcpservers.yaml \
             muster.giantswarm.io_serviceclasses.yaml \
             muster.giantswarm.io_workflows.yaml

##@ CRD Management

.PHONY: generate-crds
generate-crds: ## Generate CRDs from Go types into deploy/crds.
	@echo "====> Generating CRDs from Go types..."
	controller-gen crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:dir=$(CRD_SOURCE_DIR)
	@echo "CRDs generated in $(CRD_SOURCE_DIR)"

.PHONY: sync-crds
sync-crds: ## Copy CRDs from deploy/crds to helm/muster/crds.
	@echo "====> Syncing CRDs to helm chart..."
	@for f in $(CRD_FILES); do \
		cp $(CRD_SOURCE_DIR)/$$f $(CRD_HELM_DIR)/$$f; \
		echo "  Copied $$f"; \
	done
	@echo "CRDs synced to $(CRD_HELM_DIR)"

.PHONY: check-crds
check-crds: ## Check that deploy/crds and helm/muster/crds are identical.
	@echo "====> Checking CRD sync..."
	@exit_code=0; \
	for f in $(CRD_FILES); do \
		if ! diff -q $(CRD_SOURCE_DIR)/$$f $(CRD_HELM_DIR)/$$f > /dev/null 2>&1; then \
			echo "ERROR: CRD drift detected: $$f"; \
			echo "  $(CRD_SOURCE_DIR)/$$f differs from $(CRD_HELM_DIR)/$$f"; \
			echo "  Run 'make sync-crds' to fix."; \
			exit_code=1; \
		else \
			echo "  OK: $$f"; \
		fi; \
	done; \
	if [ $$exit_code -ne 0 ]; then \
		echo ""; \
		echo "CRD drift detected! Run 'make sync-crds' to synchronize."; \
		exit 1; \
	fi
	@echo "All CRDs are in sync."

.PHONY: update-crds
update-crds: generate-crds sync-crds ## Generate CRDs and sync them to helm chart.
	@echo "====> CRDs updated and synced successfully."
