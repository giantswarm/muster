{{/*
Expand the name of the chart.
*/}}
{{- define "muster-crds.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "muster-crds.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "muster-crds.labels" -}}
helm.sh/chart: {{ include "muster-crds.chart" . }}
app.kubernetes.io/name: {{ include "muster-crds.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- with index .Chart.Annotations "io.giantswarm.application.team" }}
application.giantswarm.io/team: {{ . | quote }}
{{- end }}
{{- end }}
