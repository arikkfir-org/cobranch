{{/* The release's name, which every object's name starts with, shortened to fit a DNS label with a suffix. */}}
{{- define "cobranch.fullname" -}}
{{- if contains .Chart.Name .Release.Name -}}
{{- .Release.Name | trunc 40 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name .Chart.Name | trunc 40 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}

{{/* Labels every object carries; pass a dict of the root context and the component. */}}
{{- define "cobranch.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .root.Chart.Name .root.Chart.Version | replace "+" "_" }}
app.kubernetes.io/version: {{ .root.Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .root.Release.Service }}
app.kubernetes.io/part-of: cobranch
{{ include "cobranch.selectorLabels" . }}
{{- end -}}

{{/* Labels that select a component's pods; pass a dict of the root context and the component. */}}
{{- define "cobranch.selectorLabels" -}}
app.kubernetes.io/name: {{ .root.Chart.Name }}
app.kubernetes.io/instance: {{ .root.Release.Name }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* An image reference; pass a dict of the root context and the image's values. */}}
{{- define "cobranch.image" -}}
{{ .image.repository }}:{{ .image.tag | default .root.Chart.AppVersion }}
{{- end -}}

{{/* A shell function for the test and the demo data's Job, given the Auth server's URL in $auth: signIn prints the
session cookie of a seeded sign-in as $1. BusyBox's wget follows the redirect and drops its headers, so nc reads the
raw response; stdin stays open until the response is in, since the server cancels a request whose client went away. */}}
{{- define "cobranch.signIn" -}}
signIn() {
  host=${auth#http://}
  (printf 'GET /sign-in?user=%s HTTP/1.0\r\nHost: %s\r\n\r\n' "$1" "$host"; sleep 2) | nc "$host" 80 | tr -d '\r' \
    | sed -n 's/^Set-Cookie: \(__[A-Za-z-]*cobranch-session=[^;]*\);.*/\1/p'
}
{{- end -}}

{{/* The bundled PostgreSQL's name, which its Service, StatefulSet and Secret share. */}}
{{- define "cobranch.postgresql" -}}
{{ include "cobranch.fullname" . }}-postgresql
{{- end -}}

{{/* The Secret that holds the bundled PostgreSQL's password. */}}
{{- define "cobranch.postgresql.secret" -}}
{{ .Values.postgresql.auth.existingSecret | default (include "cobranch.postgresql" .) }}
{{- end -}}

{{/* The restricted Pod Security Standard, for every pod: a container's half. */}}
{{- define "cobranch.containerSecurityContext" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: [ALL]
{{- end -}}

{{/* Spreads a component's pods over nodes, without refusing to schedule them when there are too few. */}}
{{- define "cobranch.spread" -}}
topologySpreadConstraints:
  - maxSkew: 1
    topologyKey: kubernetes.io/hostname
    whenUnsatisfiable: ScheduleAnyway
    labelSelector:
      matchLabels:
        {{- include "cobranch.selectorLabels" . | nindent 8 }}
{{- end -}}

{{/* Placement and annotations a component's values give its pods; pass the component's values. */}}
{{- define "cobranch.placement" -}}
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
{{- define "cobranch.checkDatabase" -}}
{{- if and (not .Values.postgresql.enabled) (not .Values.externalDatabase.existingSecret) -}}
{{- fail "Cobranch needs a database: keep postgresql.enabled, or set externalDatabase.existingSecret" -}}
{{- end -}}
{{- end -}}

{{/* The restricted Pod Security Standard, for every pod of the cobranch image: a pod's half. */}}
{{- define "cobranch.podSecurityContext" -}}
securityContext:
  runAsNonRoot: true
  runAsUser: 65532
  runAsGroup: 65532
  fsGroup: 65532
  seccompProfile:
    type: RuntimeDefault
{{- end -}}

{{/* The database's connection, as the servers and the migrate Job read it from their environment. */}}
{{- define "cobranch.databaseEnv" -}}
{{- if .Values.postgresql.enabled -}}
# libpq's variables, which pgx reads when COBRANCH_DATABASE_URL is empty: no password inside a URL to escape.
- name: PGHOST
  value: {{ include "cobranch.postgresql" . }}
- name: PGPORT
  value: "5432"
- name: PGDATABASE
  value: {{ .Values.postgresql.auth.database | quote }}
- name: PGUSER
  value: {{ .Values.postgresql.auth.username | quote }}
- name: PGPASSWORD
  valueFrom:
    secretKeyRef:
      name: {{ include "cobranch.postgresql.secret" . }}
      key: {{ .Values.postgresql.auth.secretKey }}
# In-cluster traffic, unencrypted; an external database's URL sets its own sslmode.
- name: PGSSLMODE
  value: disable
{{- else -}}
- name: COBRANCH_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Values.externalDatabase.existingSecret }}
      key: {{ .Values.externalDatabase.urlKey }}
{{- end }}
{{- end -}}

{{/* Every workload's log level, and where its traces go, as OpenTelemetry's own variables. */}}
{{- define "cobranch.telemetryEnv" -}}
{{- $t := .Values.telemetry -}}
- name: COBRANCH_LOG_LEVEL
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
{{- define "cobranch.serviceAccount" -}}
{{ include "cobranch.fullname" . }}-server
{{- end -}}


{{/* The migrate Job's spec, whose hash names the Job. */}}
{{- define "cobranch.migrateSpec" -}}
{{- $component := dict "root" . "component" "migrate" }}
{{- $values := .Values.migrate -}}
# Migrate retries a database that doesn't answer yet itself, until the deadline; a pod that fails is retried twice.
backoffLimit: 2
activeDeadlineSeconds: {{ $values.activeDeadlineSeconds }}
template:
  metadata:
    labels:
      {{- include "cobranch.labels" $component | nindent 6 }}
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
    {{- include "cobranch.podSecurityContext" . | nindent 4 }}
    {{- include "cobranch.placement" $values | nindent 4 }}
    containers:
      - name: migrate
        image: {{ include "cobranch.image" (dict "root" . "image" .Values.images.cobranch) }}
        imagePullPolicy: {{ .Values.images.cobranch.pullPolicy }}
        args: [migrate]
        env:
          {{- include "cobranch.telemetryEnv" . | nindent 10 }}
          {{- include "cobranch.databaseEnv" . | nindent 10 }}
          {{- if .Values.seed.enabled }}
          # Demo data, for previews only.
          - name: COBRANCH_SEED
            value: "true"
          {{- end }}
        resources:
          {{- toYaml $values.resources | nindent 10 }}
        securityContext:
          {{- include "cobranch.containerSecurityContext" . | nindent 10 }}
{{- end -}}
{{/* The workloads' public URLs, which every server reads: where the Console's pages find each API, and together the
hosts the session cookie is set for. */}}
{{- define "cobranch.urlsEnv" -}}
{{- range $name, $url := dict "CONSOLE" .Values.urls.console "REST" .Values.urls.rest "GRAPHQL" .Values.urls.graphql "AUTH" .Values.urls.auth "ADMIN" .Values.urls.admin "BACK_OFFICE" .Values.urls.backOffice }}
{{- if $url }}
- name: COBRANCH_{{ $name }}_URL
  value: {{ $url | quote }}
{{- end }}
{{- end }}
{{- end -}}

{{/* Where the REST and GraphQL servers read repositories' trees and files: the Git server's internal port, through
its own Service, which no route names. Nothing without a Git server. */}}
{{- define "cobranch.gitInternalEnv" -}}
{{- if .Values.urls.git }}
- name: COBRANCH_GIT_INTERNAL_URL
  value: {{ printf "http://%s-git-server-internal" (include "cobranch.fullname" .) | quote }}
{{- end }}
{{- end -}}

{{/* The Secret that holds the secrets key. */}}
{{- define "cobranch.secretsKey.secret" -}}
{{ .Values.secretsKey.existingSecret | default (printf "%s-secrets-key" (include "cobranch.fullname" .)) }}
{{- end -}}

{{/* The Secret that holds the Git server's SSH host key: the operator's, or the one the chart generates. */}}
{{- define "cobranch.gitSSHHostKey.secret" -}}
{{ .Values.gitServer.ssh.hostKey.existingSecret | default (printf "%s-git-ssh-host-key" (include "cobranch.fullname" .)) }}
{{- end -}}

{{/* The secrets key, which the Auth and Admin servers read: it seals the Back-office's providers' client secrets. */}}
{{- define "cobranch.secretsKeyEnv" -}}
- name: COBRANCH_SECRETS_KEY
  valueFrom:
    secretKeyRef:
      name: {{ include "cobranch.secretsKey.secret" . }}
      key: {{ .Values.secretsKey.secretKey }}
{{- end -}}

{{/* The values' identity providers, without their secrets: the Auth server signs in with them, and the Admin server
shows them. */}}
{{- define "cobranch.providersEnv" -}}
{{- $providers := list }}
{{- range .Values.auth.providers }}
{{- $providers = append $providers (dict "id" .id "name" (.name | default .id) "issuer" (.issuer | default "") "preset" (.preset | default "") "clientId" .clientId) }}
{{- end -}}
- name: COBRANCH_AUTH_PROVIDERS
  value: {{ $providers | toJson | quote }}
{{- end -}}

{{/* Sign-in's settings, which the Auth server alone reads: the providers (their secrets from your Secrets) and the
secrets key, the first administrators and the sign-up settings. */}}
{{- define "cobranch.authEnv" -}}
{{ include "cobranch.providersEnv" . }}
{{ include "cobranch.secretsKeyEnv" . }}
{{- range .Values.auth.providers }}
- name: COBRANCH_AUTH_CLIENT_SECRET_{{ .id | upper | replace "-" "_" }}
  valueFrom:
    secretKeyRef:
      name: {{ required (printf "auth.providers[%s].clientSecret.secretName names the Secret that holds the client's secret" .id) .clientSecret.secretName }}
      key: {{ .clientSecret.key | default "client-secret" }}
{{- end }}
{{- include "cobranch.signUpEnv" . }}
{{- with .Values.auth.admins }}
- name: COBRANCH_AUTH_ADMINS
  value: {{ join "," . | quote }}
{{- end }}
{{- if .Values.seed.enabled }}
# Demo data, for previews only: the sign-in page signs in as its users.
- name: COBRANCH_SEED
  value: "true"
{{- end }}
{{- end -}}

{{/* The sign-up settings the values set, which the Auth server follows and the Admin server shows as locked; those
they leave unset are the Back-office's. */}}
{{- define "cobranch.signUpEnv" -}}
{{- with .Values.auth.signUp }}
- name: COBRANCH_AUTH_SIGN_UP
  value: {{ . | quote }}
{{- with $.Values.auth.signUpDomains }}
- name: COBRANCH_AUTH_SIGN_UP_DOMAINS
  value: {{ join "," . | quote }}
{{- end }}
{{- end }}
{{- if not (kindIs "invalid" .Values.organizations.selfServe) }}
- name: COBRANCH_ORGANIZATIONS_SELF_SERVE
  value: {{ .Values.organizations.selfServe | quote }}
{{- end }}
{{- end -}}

{{/* The limits the values set, which the API servers and the Git server hold creations and pushes to; those they
leave unset are the built-in defaults. */}}
{{- define "cobranch.limitsEnv" -}}
{{- with .Values.limits }}
{{- if not (kindIs "invalid" .organizationsPerUser) }}
- name: COBRANCH_LIMITS_ORGANIZATIONS_PER_USER
  value: {{ .organizationsPerUser | int64 | toString | quote }}
{{- end }}
{{- if not (kindIs "invalid" .repositoriesPerOrganization) }}
- name: COBRANCH_LIMITS_REPOSITORIES_PER_ORGANIZATION
  value: {{ .repositoriesPerOrganization | int64 | toString | quote }}
{{- end }}
{{- if not (kindIs "invalid" .gitDataPerOrganization) }}
- name: COBRANCH_LIMITS_GIT_DATA_PER_ORGANIZATION
  value: {{ .gitDataPerOrganization | toString | quote }}
{{- end }}
{{- end }}
{{- end -}}
