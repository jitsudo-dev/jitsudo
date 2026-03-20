{{/*
Expand the name of the chart.
*/}}
{{- define "jitsudo.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "jitsudo.fullname" -}}
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
Create chart label.
*/}}
{{- define "jitsudo.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "jitsudo.labels" -}}
helm.sh/chart: {{ include "jitsudo.chart" . }}
{{ include "jitsudo.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "jitsudo.selectorLabels" -}}
app.kubernetes.io/name: {{ include "jitsudo.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "jitsudo.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "jitsudo.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Database URL: either from inline value or referenced secret.
*/}}
{{- define "jitsudo.databaseURLEnv" -}}
{{- if .Values.config.database.existingSecret }}
- name: JITSUDOD_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Values.config.database.existingSecret }}
      key: DATABASE_URL
{{- else if .Values.config.database.url }}
- name: JITSUDOD_DATABASE_URL
  value: {{ .Values.config.database.url | quote }}
{{- else if .Values.postgresql.enabled }}
- name: JITSUDOD_DATABASE_URL
  value: {{ printf "postgres://%s:%s@%s-postgresql:5432/%s?sslmode=disable"
    .Values.postgresql.auth.username
    .Values.postgresql.auth.password
    (include "jitsudo.fullname" .)
    .Values.postgresql.auth.database | quote }}
{{- end }}
{{- end }}
