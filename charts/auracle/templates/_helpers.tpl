{{- define "auracle.name" -}}
{{- .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "auracle.fullname" -}}
{{- if contains .Chart.Name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}

{{- define "auracle.selectorLabels" -}}
app.kubernetes.io/name: {{ include "auracle.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{- define "auracle.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
{{ include "auracle.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/* Name of the PersistentVolumeClaim holding the docs, if any. */}}
{{- define "auracle.docsClaim" -}}
{{- if .Values.docs.existingClaim }}
{{- .Values.docs.existingClaim }}
{{- else if .Values.docs.persistence.enabled }}
{{- printf "%s-docs" (include "auracle.fullname" .) }}
{{- end }}
{{- end }}
