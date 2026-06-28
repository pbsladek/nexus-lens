{{- define "nexus-lens.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "nexus-lens.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "nexus-lens.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

