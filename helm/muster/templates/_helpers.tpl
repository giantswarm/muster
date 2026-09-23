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
Create chart name and version as used by the chart label. A label value is at
most 63 characters and begins and ends alphanumeric: the cut of a long version
(a branch build's <version>-dev.<branch>.<date>.<time>.<sha>, or the
<version>+<digest> helm-controller installs) can land on any run of ".", "_"
(from "+") and "-", so the whole run is trimmed.
*/}}
{{- define "muster.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimAll "-._" }}
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
application.giantswarm.io/team: {{ index .Chart.Annotations "io.giantswarm.application.team" | quote }}
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
Effective metric exporter list: muster.observability.metrics.exporter,
normalized (whitespace-trimmed, empty elements dropped), with "prometheus"
appended when prometheus.serviceMonitor.enabled is set — asking for a
ServiceMonitor implies serving /metrics, so the "none" no-op sentinel is
dropped in that case too.
*/}}
{{- define "muster.effectiveMetricsExporter" -}}
{{- $exporters := list -}}
{{- range (.Values.muster.observability.metrics.exporter | default "" | toString | splitList ",") -}}
{{- with trim . -}}
{{- $exporters = append $exporters . -}}
{{- end -}}
{{- end -}}
{{- if .Values.muster.observability.metrics.prometheus.serviceMonitor.enabled -}}
{{- $exporters = without $exporters "none" -}}
{{- if not (has "prometheus" $exporters) -}}
{{- $exporters = append $exporters "prometheus" -}}
{{- end -}}
{{- end -}}
{{- join "," $exporters -}}
{{- end }}

{{/*
Render "true" when "prometheus" is an exact comma-separated element of
the effective exporter list. Distinguishes "prometheus" /
"otlp,prometheus" / "otlp, prometheus" (enabled) from "fakeprometheus" /
"prometheus_dev" (not enabled).
*/}}
{{- define "muster.prometheusExporterEnabled" -}}
{{- if has "prometheus" (splitList "," (include "muster.effectiveMetricsExporter" .)) -}}true{{- end -}}
{{- end }}

{{/*
Data of the OAuth credentials Secret the chart renders (templates/oauth-secret.yaml),
one "key: base64" line per entry: the provider's client secret (and Google's
client id), the registration token, the token encryption key and, with Valkey
storage and no Valkey Secret of its own, the Valkey password. Its SHA-256 is the
pod template's checksum/oauth-secret annotation, so muster rolls when one of
them changes; the Secret template and the annotation share it so the two
cannot drift. The gates fail the render the way the Secret template did.
*/}}
{{- define "muster.oauthSecretData" -}}
{{- $s := .Values.muster.oauth.server -}}
{{- $lines := list -}}
{{- if eq ($s.provider | default "dex") "google" -}}
{{- if $s.google.clientID -}}
{{- $lines = append $lines (printf "google-client-id: %s" ($s.google.clientID | b64enc | quote)) -}}
{{- else -}}
{{- fail "muster.oauth.server.google.clientID is required when Google provider is used and existingSecret is not set" -}}
{{- end -}}
{{- if $s.google.clientSecret -}}
{{- $lines = append $lines (printf "google-client-secret: %s" ($s.google.clientSecret | b64enc | quote)) -}}
{{- else -}}
{{- fail "muster.oauth.server.google.clientSecret is required when Google provider is used and existingSecret is not set" -}}
{{- end -}}
{{- else if eq ($s.provider | default "dex") "dex" -}}
{{- if $s.dex.clientSecret -}}
{{- $lines = append $lines (printf "dex-client-secret: %s" ($s.dex.clientSecret | b64enc | quote)) -}}
{{- else -}}
{{- fail "muster.oauth.server.dex.clientSecret is required when Dex provider is used and existingSecret is not set" -}}
{{- end -}}
{{- end -}}
{{- if not $s.allowPublicClientRegistration -}}
{{- if $s.registrationToken -}}
{{- $lines = append $lines (printf "registration-token: %s" ($s.registrationToken | b64enc | quote)) -}}
{{- else if and (not $s.trustedPublicRegistrationSchemes) (not $s.trustedPublicRegistrationRedirectURIs) -}}
{{- fail "OAuth client registration has no gate configured. Set one of: registrationToken | allowPublicClientRegistration=true | trustedPublicRegistrationSchemes | trustedPublicRegistrationRedirectURIs | existingSecret." -}}
{{- end -}}
{{- end -}}
{{- if $s.encryptionKey -}}
{{- if $s.encryptionKeyValue -}}
{{- $lines = append $lines (printf "oauth-encryption-key: %s" ($s.encryptionKeyValue | b64enc | quote)) -}}
{{- else -}}
{{- fail "muster.oauth.server.encryptionKeyValue is required when encryptionKey is true and existingSecret is not set" -}}
{{- end -}}
{{- end -}}
{{- if and (eq $s.storage.type "valkey") (not $s.storage.valkey.existingSecret) $s.storage.valkey.password -}}
{{- $lines = append $lines (printf "valkey-password: %s" ($s.storage.valkey.password | b64enc | quote)) -}}
{{- end -}}
{{- join "\n" $lines -}}
{{- end -}}

{{/*
Value of the pod template's checksum/oauth-secret annotation: the SHA-256 of the
chart-rendered Secret's data, or muster.oauth.server.existingSecretChecksum
verbatim when the credentials come from an existing Secret the chart cannot
read. Empty while the OAuth server is off, or while nothing marks the existing
Secret's revision.
*/}}
{{- define "muster.oauthSecretChecksum" -}}
{{- $s := .Values.muster.oauth.server -}}
{{- if $s.enabled -}}
{{- if $s.existingSecret -}}
{{- $s.existingSecretChecksum -}}
{{- else -}}
{{- include "muster.oauthSecretData" . | sha256sum -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Value of the pod template's checksum/valkey-secret annotation:
muster.oauth.server.storage.valkey.existingSecretChecksum verbatim, for a
Valkey password in a Secret of its own that the chart cannot read. Empty
otherwise: a password in the OAuth Secret is covered by checksum/oauth-secret.
*/}}
{{- define "muster.valkeySecretChecksum" -}}
{{- $s := .Values.muster.oauth.server -}}
{{- if and $s.enabled (eq $s.storage.type "valkey") $s.storage.valkey.existingSecret -}}
{{- $s.storage.valkey.existingSecretChecksum -}}
{{- end -}}
{{- end -}}
