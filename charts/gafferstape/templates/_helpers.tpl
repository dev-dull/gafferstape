{{/*
Chart name, truncated to 63 chars (DNS-1123 label limit).
*/}}
{{- define "gafferstape.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. If the release name already contains the chart
name, just use the release name; otherwise prefix it.
*/}}
{{- define "gafferstape.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Chart label — "<chart>-<version>".
*/}}
{{- define "gafferstape.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Selector labels (used by Deployment.spec.selector + Service.spec.selector).
*/}}
{{- define "gafferstape.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gafferstape.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Common labels (applied to every object).
*/}}
{{- define "gafferstape.labels" -}}
helm.sh/chart: {{ include "gafferstape.chart" . }}
{{ include "gafferstape.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{/*
ServiceAccount name — either an explicit override, the chart-generated
name, or "default" if SA creation is disabled.
*/}}
{{- define "gafferstape.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "gafferstape.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Resolved Secret name — either an externally-managed one or the
chart-rendered one.
*/}}
{{- define "gafferstape.secretName" -}}
{{- if .Values.tokens.existingSecret -}}
{{- .Values.tokens.existingSecret -}}
{{- else -}}
{{- include "gafferstape.fullname" . -}}
{{- end -}}
{{- end -}}
