{{/*
muster-crds intentionally ships no templated Kubernetes resources —
Helm 3 reads the chart's `crds/` directory directly. This file exists
to satisfy app-build-suite's GiantSwarmHelmValidator, which requires a
`templates/_helpers.tpl` (or `_helpers.yaml`) in every Giant Swarm chart.
*/}}
{{- define "muster-crds.labels" -}}
application.giantswarm.io/team: {{ index .Chart.Annotations "io.giantswarm.application.team" | quote }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service | quote }}
{{- end }}
