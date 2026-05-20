# CRD Generation Targets
#
# CRDs are written to helm/muster/files/crds/ and rendered by the chart
# loader at helm/muster/templates/crds.yaml. `files/` is plain chart-bundled
# content (no Helm 3 `crds/` special-case), so the rendering path is the
# same whether muster is installed standalone or consumed as a sub-chart.

CRD_DIR := helm/muster/files/crds

##@ CRD Management

.PHONY: generate-crds
generate-crds: ## Generate CRDs from Go types into helm/muster/files/crds.
	@echo "====> Generating CRDs from Go types..."
	controller-gen crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:dir=$(CRD_DIR)
	@echo "CRDs generated in $(CRD_DIR)"
