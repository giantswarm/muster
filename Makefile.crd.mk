# CRD Generation Targets
#
# CRDs are written to helm/muster-crds/files/crds/ and rendered by the chart
# loader at helm/muster-crds/templates/crds.yaml. They live in the standalone
# `muster-crds` chart so the CRD lifecycle is decoupled from the muster
# application chart. `files/` is plain chart-bundled content (no Helm 3 `crds/`
# special-case), so the CRDs are upgradable on `helm upgrade`.

CRD_DIR := helm/muster-crds/files/crds

##@ CRD Management

.PHONY: generate-crds
generate-crds: ## Generate CRDs from Go types into helm/muster-crds/files/crds.
	@echo "====> Generating CRDs from Go types..."
	controller-gen crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:dir=$(CRD_DIR)
	@echo "CRDs generated in $(CRD_DIR)"
