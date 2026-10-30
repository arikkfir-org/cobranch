{{/* The release's name, which every object's name starts with, shortened to fit a DNS label with a suffix. */}}
{{- define "devhub.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 40 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 40 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Labels every object carries; pass a dict of the root context and the component. */}}
{{- define "devhub.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/part-of: devhub
{{ include "devhub.selectorLabels" . }}
{{- end -}}

{{/* Labels that select a component's pods; pass a dict of the root context and the component. */}}
{{- define "devhub.selectorLabels" -}}
app.kubernetes.io/name: {{ .root.Chart.Name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* An image reference; pass a dict of the root context and the image's values. */}}
{{- define "devhub.image" -}}
{{ .image.repository }}:{{ .image.tag | default .root.Chart.AppVersion }}
{{- end -}}

{{/* The bundled PostgreSQL's name, which its Service, StatefulSet and Secret share. */}}
{{- define "devhub.postgresql" -}}
{{ include "devhub.fullname" . }}-postgresql
{{- end -}}

{{/* The Secret that holds the bundled PostgreSQL's password. */}}
{{- define "devhub.postgresql.secret" -}}
{{ .Values.postgresql.auth.existingSecret | default (include "devhub.postgresql" .) }}
{{- end -}}

{{/* The restricted Pod Security Standard, for every pod: a container's half. */}}
{{- define "devhub.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
{{- end -}}

{{/* Spreads a component's pods over nodes, without refusing to schedule them when there are too few. */}}
{{- define "devhub.spread" -}}
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway
    labelSelector:
      matchLabels:
        {{- include "devhub.selectorLabels" . | nindent 8 }}
{{- end -}}

{{/* Placement and annotations a component's values give its pods; pass the component's values. */}}
{{- define "devhub.placement" -}}
{{- with .nodeSelector }}
nodeSelector:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .tolerations }}
tolerations:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- with .affinity }}
affinity:
  {{- toYaml . | nindent 2 }}
{{- end }}
{{- end -}}

{{/* Fails the render unless the servers and the migrate Job have a database. */}}
{{- define "devhub.checkDatabase" -}}
{{- if and (not .Values.postgresql.enabled) (not .Values.externalDatabase.existingSecret) -}}
{{- fail "DevHub needs a database: keep postgresql.enabled, or set externalDatabase.existingSecret" -}}
{{- end -}}
{{- end -}}

{{/* The restricted Pod Security Standard, for every pod of the devhub image: a pod's half. */}}
{{- define "devhub.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{/* The database's connection, as the servers and the migrate Job read it from their environment. */}}
{{- define "devhub.databaseEnv" -}}
{{- if .Values.postgresql.enabled -}}
# libpq's variables, which pgx reads when DEVHUB_DATABASE_URL is empty: no password inside a URL to escape.
- name: PGHOST
  value: {{ include "devhub.postgresql" . }}
- name: PGPORT
  value: "5432"
- name: PGDATABASE
  value: {{ .Values.postgresql.auth.database | quote }}
- name: PGUSER
  value: {{ .Values.postgresql.auth.username | quote }}
- name: PGPASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "devhub.postgresql.secret" . }}
      key: {{ .Values.postgresql.auth.secretKey }}
# In-cluster traffic, unencrypted; an external database's URL sets its own sslmode.
- name: PGSSLMODE
  value: disable
{{- else -}}
- name: DEVHUB_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Values.externalDatabase.existingSecret }}
      key: {{ .Values.externalDatabase.urlKey }}
{{- end }}
{{- end -}}

{{/* Every workload's log level, and where its traces go, as OpenTelemetry's own variables. */}}
{{- define "devhub.telemetryEnv" -}}
{{- $t := .Values.telemetry -}}
- name: DEVHUB_LOG_LEVEL
  value: {{ $t.logLevel | quote }}
{{- with $t.traces.endpoint }}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ . | quote }}
- name: OTEL_TRACES_SAMPLER
  value: parentbased_traceidratio
- name: OTEL_TRACES_SAMPLER_ARG
  value: {{ $t.traces.sampleRatio | quote }}
{{- end }}
{{- with $t.traces.headersSecret }}
- name: OTEL_EXPORTER_OTLP_HEADERS
  valueFrom:
    secretKeyRef:
      name: {{ . }}
      key: {{ $t.traces.headersKey }}
{{- end }}
{{- range $name, $value := $t.env }}
- name: {{ $name }}
  value: {{ $value | quote }}
{{- end }}
{{- end -}}

{{/* The servers' ServiceAccount, which both share. */}}
{{- define "devhub.serviceAccount" -}}
{{ include "devhub.fullname" . }}-server
{{- end -}}


{{/* The migrate Job's spec, whose hash names the Job. */}}
{{- define "devhub.migrateSpec" -}}
{{- $component := dict "root" . "component" "migrate" }}
{{- $values := .Values.migrate -}}
# Migrate retries a database that doesn't answer yet itself, until the deadline; a pod that fails is retried twice.
backoffLimit: 2
activeDeadlineSeconds: {{ $values.activeDeadlineSeconds }}
template:
  metadata:
    labels:
      {{- include "devhub.labels" $component | nindent 6 }}
    {{- with $values.podAnnotations }}
    annotations:
      {{- toYaml . | nindent 6 }}
    {{- end }}
  spec:
    restartPolicy: Never
    automountServiceAccountToken: false
    {{- with .Values.imagePullSecrets }}
    imagePullSecrets:
      {{- toYaml . | nindent 6 }}
    {{- end }}
    {{- include "devhub.podSecurityContext" . | nindent 4 }}
    {{- include "devhub.placement" $values | nindent 4 }}
    containers:
      - name: migrate
        image: {{ include "devhub.image" (dict "root" . "image" .Values.images.devhub) }}
        imagePullPolicy: {{ .Values.images.devhub.pullPolicy }}
        args: [migrate]
        env:
          {{- include "devhub.telemetryEnv" . | nindent 10 }}
          {{- include "devhub.databaseEnv" . | nindent 10 }}
          {{- if .Values.seed.enabled }}
          # Demo data, for previews only.
          - name: DEVHUB_SEED
            value: "true"
          {{- end }}
        resources:
          {{- toYaml $values.resources | nindent 10 }}
        securityContext:
          {{- include "devhub.containerSecurityContext" . | nindent 10 }}
{{- end -}}