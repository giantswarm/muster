# CRD Generation Targets
#
# This Makefile extension provides targets to generate CRDs from Go types.
# CRDs are generated into helm/muster-crds/crds/, which is the single
# source of truth and the chart that ships them to clusters.

# CRD directory (single location)
CRD_DIR := helm/muster-crds/crds

##@ CRD Management

.PHONY: generate-crds
generate-crds: ## Generate CRDs from Go types into helm/muster/crds.
	@echo "====> Generating CRDs from Go types..."
	controller-gen crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:dir=$(CRD_DIR)
	@echo "CRDs generated in $(CRD_DIR)"
