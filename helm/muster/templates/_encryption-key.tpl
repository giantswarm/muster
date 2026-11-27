{{- define "muster.requireOwnedEncryptionSecret" -}}
{{- $root := index . 0 -}}
{{- $secret := index . 1 -}}
{{- $owner := dig "metadata" "annotations" "meta.helm.sh/release-name" "" $secret -}}
{{- $namespace := dig "metadata" "annotations" "meta.helm.sh/release-namespace" "" $secret -}}
{{- if or (ne $owner $root.Release.Name) (ne $namespace $root.Release.Namespace) -}}
{{- fail (printf "Secret %s/%s is not owned by this release; set muster.oauth.server.encryptionKeySecret.name to use an externally managed key" $root.Release.Namespace $secret.metadata.name) -}}
{{- end -}}
{{- end -}}

{{/* A render resolves one key source and one checksum for every replica. */}}
{{- define "muster.encryptionKey" -}}
{{- if hasKey .Values "_musterEncryptionKey" -}}
{{- get .Values "_musterEncryptionKey" | toJson -}}
{{- else -}}
{{- $server := .Values.muster.oauth.server -}}
{{- $reference := $server.encryptionKeySecret -}}
{{- $name := printf "%s-encryption-key" (include "muster.fullname" .) -}}
{{- $source := dict "name" $name "key" "oauth-encryption-key" "managed" true "data" "" "checksum" "" -}}
{{- $stored := dict -}}
{{- if $reference.name -}}
{{- $_ := set $source "name" $reference.name -}}
{{- $_ := set $source "key" ($reference.key | default "oauth-encryption-key") -}}
{{- $_ := set $source "managed" false -}}
{{- $_ := set $source "checksum" $reference.checksum -}}
{{- $stored = lookup "v1" "Secret" .Release.Namespace $reference.name | default dict -}}
{{- else -}}
{{- $stored = lookup "v1" "Secret" .Release.Namespace $name | default dict -}}
{{- if $stored -}}
{{- include "muster.requireOwnedEncryptionSecret" (list . $stored) -}}
{{- else -}}
{{- $credentialsName := $server.existingSecret | default (printf "%s-oauth" (include "muster.fullname" .)) -}}
{{- $credentials := lookup "v1" "Secret" .Release.Namespace $credentialsName | default dict -}}
{{- if hasKey ($credentials.data | default dict) "oauth-encryption-key" -}}
{{- $stored = $credentials -}}
{{- if $server.existingSecret -}}
{{- $_ := set $source "name" $credentialsName -}}
{{- $_ := set $source "managed" false -}}
{{- $_ := set $source "checksum" $server.existingSecretChecksum -}}
{{- else -}}
{{- include "muster.requireOwnedEncryptionSecret" (list . $credentials) -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- if and $source.managed $server.encryptionKeyValue -}}
{{- $_ := set $source "data" ($server.encryptionKeyValue | b64enc) -}}
{{- else if $stored -}}
{{- $data := $stored.data | default dict -}}
{{- $_ := set $source "data" (required (printf "Secret %s/%s must contain a non-empty %s; restore the encryption key or rotate it explicitly" .Release.Namespace $stored.metadata.name $source.key) (get $data $source.key)) -}}
{{- else if $source.managed -}}
{{- $_ := set $source "data" (randBytes 32 | b64enc) -}}
{{- end -}}
{{- if and (not $source.checksum) $source.data -}}
{{- $_ := set $source "checksum" ($source.data | sha256sum) -}}
{{- end -}}
{{- $_ := set .Values "_musterEncryptionKey" $source -}}
{{- $source | toJson -}}
{{- end -}}
{{- end -}}
