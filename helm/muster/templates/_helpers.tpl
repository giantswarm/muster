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

Returns the canonical list of derived (cluster, role) tbot outputs as a
YAML list of dicts. Each entry has:
  name        — unique key (used for collision detection: "<role>-<cluster>")
  appName     — Teleport application name ("<role>-<cluster>")
  secretName  — Kubernetes Secret name written by tbot
                ("tbot-identity-mcp-<cluster>" or "tbot-identity-tx-<cluster>")

Per PLAN §6 TB-4 the role tags in secret names are abbreviated:
  mcp-kubernetes  -> "mcp" (secret tbot-identity-mcp-<cluster>)
  dex             -> "tx"  (secret tbot-identity-tx-<cluster>)

The Teleport-side appName uses the full role name ("mcp-kubernetes-<cluster>",
"dex-<cluster>") because that name is what matches the Teleport apps list
locked in TB-1/TB-2.

apps[] entries override clusters[]-derived entries with the same `name` key
(escape hatch for non-conformant Teleport-side names). Duplicate names within
apps[] are a templating-time error.
*/}}
{{- define "muster.tbot.outputs" -}}
{{- $cfg := .Values.transport.teleport -}}
{{- $derived := list -}}
{{- range $idx, $cluster := (default (list) $cfg.clusters) -}}
  {{- if not $cluster.name -}}
    {{- fail (printf "transport.teleport.clusters[%d].name is required" $idx) -}}
  {{- end -}}
  {{- $derived = append $derived (dict
      "name" (printf "mcp-kubernetes-%s" $cluster.name)
      "appName" (printf "mcp-kubernetes-%s" $cluster.name)
      "secretName" (printf "tbot-identity-mcp-%s" $cluster.name)
  ) -}}
  {{- $derived = append $derived (dict
      "name" (printf "dex-%s" $cluster.name)
      "appName" (printf "dex-%s" $cluster.name)
      "secretName" (printf "tbot-identity-tx-%s" $cluster.name)
  ) -}}
{{- end -}}
{{/* Detect duplicates within apps[] (template-time fail per PLAN §6 TB-4). */}}
{{- $seen := dict -}}
{{- range $idx, $a := (default (list) $cfg.apps) -}}
  {{- if not $a.name -}}
    {{- fail (printf "transport.teleport.apps[%d].name is required" $idx) -}}
  {{- end -}}
  {{- if hasKey $seen $a.name -}}
    {{- fail (printf "transport.teleport.apps: duplicate entry name %q" $a.name) -}}
  {{- end -}}
  {{- $_ := set $seen $a.name true -}}
{{- end -}}
{{/* Index derived list by name for override lookup. */}}
{{- $byName := dict -}}
{{- range $derived -}}
  {{- $_ := set $byName .name . -}}
{{- end -}}
{{/* Apply apps[] overrides (replace same-name entries from clusters[]). */}}
{{- range $a := (default (list) $cfg.apps) -}}
  {{- $entry := dict
      "name" $a.name
      "appName" (default $a.name $a.appName)
      "secretName" (required (printf "transport.teleport.apps[%s].secretName is required" $a.name) $a.secretName)
  -}}
  {{- $_ := set $byName $a.name $entry -}}
{{- end -}}
{{/* Emit a stable, ordered list. clusters[] entries first, in definition order;
     then any apps[]-only entries (those whose name was not produced by clusters[]). */}}
{{- $emitted := dict -}}
{{- range $derived -}}
  {{- $entry := index $byName .name -}}
  {{- $_ := set $emitted .name true -}}
- name: {{ $entry.name | quote }}
  appName: {{ $entry.appName | quote }}
  secretName: {{ $entry.secretName | quote }}
{{ end -}}
{{- range $a := (default (list) $cfg.apps) -}}
  {{- if not (hasKey $emitted $a.name) -}}
    {{- $entry := index $byName $a.name }}
- name: {{ $entry.name | quote }}
  appName: {{ $entry.appName | quote }}
  secretName: {{ $entry.secretName | quote }}
{{ end -}}
{{- end -}}
{{- end }}

