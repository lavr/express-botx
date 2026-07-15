{{/*
Expand the name of the chart.
*/}}
{{- define "express-botx.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "express-botx.fullname" -}}
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
{{- define "express-botx.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "express-botx.labels" -}}
helm.sh/chart: {{ include "express-botx.chart" . }}
{{ include "express-botx.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "express-botx.selectorLabels" -}}
app.kubernetes.io/name: {{ include "express-botx.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use.
*/}}
{{- define "express-botx.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "express-botx.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Config secret name.
*/}}
{{- define "express-botx.secretName" -}}
{{- default (include "express-botx.fullname" .) .Values.existingSecret }}
{{- end }}

{{- define "express-botx.portName" -}}
{{- if .Values.tls.enabled }}https{{ else }}http{{ end }}
{{- end }}

{{- define "express-botx.tls.secretName" -}}
{{- if .Values.tls.existingSecret -}}
{{- .Values.tls.existingSecret -}}
{{- else -}}
{{- printf "%s-tls" (include "express-botx.fullname" .) -}}
{{- end -}}
{{- end }}

{{- define "express-botx.tls.dnsNames" -}}
{{- $names := list -}}
{{- range .Values.tls.certManager.dnsNames -}}
  {{- $name := trim (toString (default "" .)) -}}
  {{- if $name }}{{- $names = append $names $name -}}{{- end -}}
{{- end -}}
{{- if and (eq (len $names) 0) .Values.ingress.enabled -}}
  {{- range .Values.ingress.hosts -}}
    {{- $hostConfig := default dict . -}}
    {{- $host := trim (toString (default "" $hostConfig.host)) -}}
    {{- if $host }}{{- $names = append $names $host -}}{{- end -}}
  {{- end -}}
{{- end -}}
{{- if eq (len $names) 0 -}}
  {{- fail "tls.certManager.dnsNames is required (or enable ingress with non-empty hosts)" -}}
{{- end -}}
{{- range $names }}
- {{ . | quote }}
{{- end -}}
{{- end }}

{{- define "express-botx.tls.validate" -}}
{{- if .Values.tls.enabled -}}
  {{- if eq .Values.mode "worker" }}{{- fail "tls.enabled is not supported with mode=worker" -}}{{- end -}}
  {{- $cm := .Values.tls.certManager.enabled -}}
  {{- $tlsSecret := trim (toString (default "" .Values.tls.existingSecret)) -}}
  {{- $issuerRef := default dict .Values.tls.certManager.issuerRef -}}
  {{- if and $cm $tlsSecret }}{{- fail "tls.certManager.enabled and tls.existingSecret are mutually exclusive" -}}{{- end -}}
  {{- if and (not $cm) (not $tlsSecret) }}{{- fail "tls.enabled requires tls.certManager.enabled or tls.existingSecret" -}}{{- end -}}
  {{- if and $cm (not (trim (toString (default "" $issuerRef.name)))) }}{{- fail "tls.certManager.issuerRef.name is required" -}}{{- end -}}

  {{- $reloadInterval := trim (toString (default "" .Values.tls.reloadInterval)) -}}
  {{- $durationPattern := "^[+]?(([0-9]+([.][0-9]*)?|[.][0-9]+)(ns|us|µs|μs|ms|s|m|h))+$" -}}
  {{- if or (not (regexMatch $durationPattern $reloadInterval)) (not (regexMatch "[1-9]" $reloadInterval)) -}}
    {{- fail (printf "tls.reloadInterval must be a positive Go duration (for example 60s), got %q" $reloadInterval) -}}
  {{- end -}}

  {{- $container := toString .Values.containerPort -}}
  {{- $target := toString .Values.service.targetPort -}}
  {{- $portName := include "express-botx.portName" . -}}
  {{- if and (ne $target $container) (ne $target $portName) -}}
    {{- fail (printf "service.targetPort must equal containerPort (%s) or port name %q when TLS is enabled" $container $portName) -}}
  {{- end -}}

  {{- $listenEnv := false -}}
  {{- range .Values.extraEnv -}}
    {{- if eq (default "" .name) "EXPRESS_BOTX_SERVER_LISTEN" }}{{- $listenEnv = true -}}{{- end -}}
  {{- end -}}
  {{- if and (not .Values.configRaw) (not .Values.existingSecret) (not $listenEnv) -}}
    {{- $config := default dict .Values.config -}}
    {{- $server := default dict $config.server -}}
    {{- $listen := toString (default "" $server.listen) -}}
    {{- $suffix := regexFind ":[0-9]+$" $listen -}}
    {{- if $suffix -}}
      {{- $listenPort := trimPrefix ":" $suffix -}}
      {{- if ne $listenPort $container -}}
        {{- fail (printf "config.server.listen port (%s) must equal containerPort (%s) when TLS is enabled" $listenPort $container) -}}
      {{- end -}}
    {{- end -}}
  {{- end -}}
{{- end -}}
{{- end }}
