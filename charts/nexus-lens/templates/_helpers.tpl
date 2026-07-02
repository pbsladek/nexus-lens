{{- define "nexus-lens.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "nexus-lens.fullname" -}}
{{- printf "%s-%s" .Release.Name (include "nexus-lens.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "nexus-lens.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" }}
app.kubernetes.io/name: {{ include "nexus-lens.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "nexus-lens.selectorLabels" -}}
app.kubernetes.io/name: {{ include "nexus-lens.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "nexus-lens.apiImage" -}}
{{- printf "%s:%s" .Values.api.image.repository (default .Values.image.tag .Values.api.image.tag) -}}
{{- end -}}

{{- define "nexus-lens.workerImage" -}}
{{- printf "%s:%s" .Values.worker.image.repository (default .Values.image.tag .Values.worker.image.tag) -}}
{{- end -}}

{{- define "nexus-lens.apiImagePullPolicy" -}}
{{- default .Values.image.pullPolicy .Values.api.image.pullPolicy -}}
{{- end -}}

{{- define "nexus-lens.workerImagePullPolicy" -}}
{{- default .Values.image.pullPolicy .Values.worker.image.pullPolicy -}}
{{- end -}}
