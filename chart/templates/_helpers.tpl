{{/*
Expand the name of the chart.
*/}}
{{- define "config-db.name" -}}
{{- .Values.nameOverride | default .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "config-db.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* Create the name of config-db service account */}}
{{- define "serviceAccountName" -}}
{{ .Values.global.serviceAccount.name | default .Values.serviceAccount.name | default ( printf "%s-sa" (include "config-db.name" .)) }}
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

{{- define "config-db.extraLabels" -}}
{{- if .Values.global.labels -}}
{{.Values.global.labels | toYaml}}
{{- end }}
{{- end }}


{{- define "resource-quantity" -}}
    {{- $value := . -}}
    {{- $unit := 1.0 -}}
    {{- if typeIs "string" . -}}
        {{- $base2 := dict "Ki" 0x1p10 "Mi" 0x1p20 "Gi" 0x1p30 "Ti" 0x1p40 "Pi" 0x1p50 "Ei" 0x1p60 -}}
        {{- $base10 := dict "m" 1e-3 "k" 1e3 "M" 1e6 "G" 1e9 "T" 1e12 "P" 1e15 "E" 1e18 -}}
        {{- range $k, $v := merge $base2 $base10 -}}
            {{- if hasSuffix $k $ -}}
                {{- $value = trimSuffix $k $ -}}
                {{- $unit = $v -}}
            {{- end -}}
        {{- end -}}
    {{- end -}}
    {{- mulf (float64 $value) $unit -}}
{{- end -}}

{{- define "gomaxprocs" -}}
    {{- with .Values.resources }}{{ with .limits }}{{ with .cpu -}}
        {{- include "resource-quantity" . | float64 | ceil | int -}}
    {{- end }}{{ end }}{{ end -}}
{{- end -}}

{{- define "gomemlimit" -}}
    {{- with .Values.resources }}{{ with .limits }}{{ with .memory -}}
        {{- $bytes :=  include "resource-quantity" . | float64 | mulf 0.80 | ceil | int -}}
        {{- divf $bytes 1024 1024 | printf "%0.0f" -}}MiB
    {{- end }}{{ end }}{{ end -}}
{{- end -}}


{{- define "truthy" -}}
{{- $a := index . 0}}
{{- $b := index . 1}}
{{- if eq "false" ($a | toString) -}}
false
{{- else -}}
{{- default $a $b -}}
{{end}}



{{- end -}}

{{- define "clickhouse.maxMemory" -}}
{{- $input := .Values.clickhouse.resources.limits.memory -}}
{{- $bytes := 0 -}}

{{- if hasSuffix "Gi" $input -}}
  {{- $number := trimSuffix "Gi" $input | int64 | float64 -}}
  {{- $bytes = mulf $number 1073741824.0 | mulf 0.6667 | floor -}}
{{- else if hasSuffix "Mi" $input -}}
  {{- $number := trimSuffix "Mi" $input | int64 | float64 -}}
  {{- $bytes = mulf $number 1048576.0 | mulf 0.6667 | floor -}}
{{- else if hasSuffix "Ki" $input -}}
  {{- $number := trimSuffix "Ki" $input | int64 | float64 -}}
  {{- $bytes = mulf $number 1024.0 | mulf 0.6667 | floor -}}
{{- else -}}
  {{- $bytes = . | int64 | float64 | mulf 0.6667 | floor -}}
{{- end -}}

{{- $bytes | printf "%.0f" -}}
{{- end -}}
