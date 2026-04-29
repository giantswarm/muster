{{/*
Expand the name of the chart.
*/}}
{{- define "muster.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "muster.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "muster.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "muster.labels" -}}
helm.sh/chart: {{ include "muster.chart" . }}
{{ include "muster.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
application.giantswarm.io/team: {{ index .Chart.Annotations "application.giantswarm.io/team" | quote }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "muster.selectorLabels" -}}
app.kubernetes.io/name: {{ include "muster.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "muster.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "muster.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Create the namespace for muster resource discovery
*/}}
{{- define "muster.namespace" -}}
{{- .Values.muster.namespace | default .Release.Namespace }}
{{- end }}

{{/*
tbot ServiceAccount name. Distinct from muster's SA — tbot authenticates
to Teleport via the kubernetes join method using a projected token from
this SA, audience-bound to the Teleport cluster.
*/}}
{{- define "muster.tbot.serviceAccountName" -}}
{{- printf "%s-tbot" (include "muster.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
tbot ConfigMap name.
*/}}
{{- define "muster.tbot.configMapName" -}}
{{- printf "%s-tbot-config" (include "muster.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
tbot Deployment name.
*/}}
{{- define "muster.tbot.deploymentName" -}}
{{- printf "%s-tbot" (include "muster.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
muster.tbot.outputs

Returns the canonical list of tbot outputs as a YAML list of dicts. Each
entry has:
  appName     — Teleport application name (verbatim from values.apps[].appName)
  secretName  — Kubernetes Secret name written by tbot (verbatim from
                values.apps[].identitySecret)

Reshape (PLAN §6 TB-0 revised 2026-04-29 + TB-4 follow-up): the chart no
longer derives app or secret names from a cluster symbol. Every entry is
stated explicitly under transport.teleport.apps[], and matches what the
MCPServer CR carries in spec.transport.teleport.{mcp,dex}.{appName,
identitySecretRef.name}.

Duplicate appName values within apps[] are a template-time error.
Missing appName or identitySecret on any apps[] entry is a template-time
error.
*/}}
{{- define "muster.tbot.outputs" -}}
{{- $cfg := .Values.transport.teleport -}}
{{- $seen := dict -}}
{{- range $idx, $a := (default (list) $cfg.apps) -}}
  {{- if not $a.appName -}}
    {{- fail (printf "transport.teleport.apps[%d].appName is required" $idx) -}}
  {{- end -}}
  {{- if not $a.identitySecret -}}
    {{- fail (printf "transport.teleport.apps[%d].identitySecret is required (appName=%q)" $idx $a.appName) -}}
  {{- end -}}
  {{- if hasKey $seen $a.appName -}}
    {{- fail (printf "transport.teleport.apps: duplicate appName %q" $a.appName) -}}
  {{- end -}}
  {{- $_ := set $seen $a.appName true -}}
{{- end -}}
{{- range $a := (default (list) $cfg.apps) }}
- appName: {{ $a.appName | quote }}
  secretName: {{ $a.identitySecret | quote }}
{{- end }}
{{- end }}
