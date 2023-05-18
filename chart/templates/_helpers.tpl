{{/*
Expand the name of the chart.
*/}}
{{- define "config-db.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "config-db.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Create the name of config-db service account */}}
{{- define "config-db.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
    {{ default (include "kube-prometheus-stack.operator.fullname" .) .Values.prometheusOperator.serviceAccount.name }}
{{- else -}}
    {{ default "default" .Values.prometheusOperator.serviceAccount.name }}
{{- end -}}
{{- end -}}

{{/*
Common labels
*/}}
{{- define "config-db.labels" -}}
helm.sh/chart: {{ include "config-db.chart" . }}
{{ include "config-db.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "config-db.selectorLabels" -}}
app.kubernetes.io/name: {{ include "config-db.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
control-plane: {{ include "config-db.name" . }}
{{- end }}

