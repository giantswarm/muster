# CRD Generation Targets
#
# CRDs are written to two locations from the same Go-type source:
#
#   helm/muster-crds/files/crds/ -- the standalone `muster-crds` chart, rendered
#     by helm/muster-crds/templates/crds.yaml. `files/` is plain chart-bundled
#     content (no Helm 3 `crds/` special-case), so the CRDs are upgradable on
#     `helm upgrade`. Kept for standalone / non-Flux consumers.
#
#   helm/muster/crds/ -- the muster application chart owns its CRDs (Helm 3
#     `crds/` special directory). Combined with Flux `install.crds: CreateReplace`
#     / `upgrade.crds: CreateReplace`, the CRDs travel with the app at the same
#     version and upgrade atomically on every release. The `helm.sh/resource-policy: keep`
#     annotation is baked in as defense-in-depth (the `crds/` dir is not pruned by
#     Helm/Flux regardless).

CRD_DIR := helm/muster-crds/files/crds
APP_CRD_DIR := helm/muster/crds

##@ CRD Management

.PHONY: generate-crds
generate-crds: ## Generate CRDs from Go types into both the muster-crds bundle and the muster app chart.
	@echo "====> Generating CRDs from Go types..."
	controller-gen crd:crdVersions=v1 paths="./pkg/apis/..." output:crd:dir=$(CRD_DIR)
	@echo "CRDs generated in $(CRD_DIR)"
	@echo "====> Syncing CRDs into $(APP_CRD_DIR) with resource-policy keep..."
	@mkdir -p $(APP_CRD_DIR)
	@for f in $(CRD_DIR)/*.yaml; do \
		sed '/controller-gen.kubebuilder.io\/version:/a\    helm.sh/resource-policy: keep' "$$f" > "$(APP_CRD_DIR)/$$(basename $$f)"; \
	done
	@echo "CRDs synced to $(APP_CRD_DIR)"
