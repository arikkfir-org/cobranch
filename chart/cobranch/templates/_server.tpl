{{/*
A server of one API: its Deployment, Service and budget. Pass a dict of the root context, the component, which names
the subcommand too, and the server's values; and, for the Git workloads, bucket (true for the one that reads packs),
service (false for one nobody calls), env (more of the container's variables, as YAML), volumes (more emptyDirs: a
name, a mountPath and the emptyDir each) and ports (more of the container's: a name and a containerPort each). Values
without replicas leave the count to an autoscaler.
*/}}
{{- define "cobranch.server" -}}
{{- $root := .root }}
{{- $component := dict "root" $root "component" .component }}
{{- $name := printf "%s-%s" (include "cobranch.fullname" $root) .component }}
{{- $values := .values }}
{{- $api := ne .component "auth-server" }}
{{- $bucket := .bucket | default false }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $name }}
  labels:
    {{- include "cobranch.labels" $component | nindent 4 }}
  annotations:
    # Argo CD rolls the servers out once the migrate Job, a wave before, is done.
    argocd.argoproj.io/sync-wave: "2"
spec:
  {{- if hasKey $values "replicas" }}
  replicas: {{ $values.replicas }}
  {{- end }}
  selector:
    matchLabels:
      {{- include "cobranch.selectorLabels" $component | nindent 6 }}
  # A new pod turns ready, its schema migrated, before an old one stops.
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        {{- include "cobranch.labels" $component | nindent 8 }}
      {{- with $values.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    spec:
      {{- if $bucket }}
      serviceAccountName: {{ include "cobranch.serviceAccount" $root }}
      {{- end }}
      automountServiceAccountToken: false
      {{- with $root.Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- include "cobranch.podSecurityContext" . | nindent 6 }}
      {{- include "cobranch.spread" $component | nindent 6 }}
      {{- include "cobranch.placement" $values | nindent 6 }}
      containers:
        - name: {{ .component }}
          image: {{ include "cobranch.image" (dict "root" $root "image" $root.Values.images.cobranch) }}
          imagePullPolicy: {{ $root.Values.images.cobranch.pullPolicy }}
          args: [{{ .component }}]
          ports:
            - name: http
              containerPort: 8080
            {{- range .ports }}
            - name: {{ .name }}
              containerPort: {{ .containerPort }}
            {{- end }}
          env:
            - name: COBRANCH_HTTP_ADDR
              value: ":8080"
            {{- if $api }}
            {{- if $bucket }}
            - name: COBRANCH_BLOB_URL
              value: {{ $root.Values.blob.url | quote }}
            {{- end }}
            {{- if eq .component "admin-server" }}
            {{- with include "cobranch.signUpEnv" $root | trim }}
            {{- . | nindent 12 }}
            {{- end }}
            {{- include "cobranch.providersEnv" $root | nindent 12 }}
            {{- include "cobranch.secretsKeyEnv" $root | nindent 12 }}
            {{- end }}
            {{- else }}
            {{- include "cobranch.authEnv" $root | nindent 12 }}
            {{- end }}
            {{- include "cobranch.urlsEnv" $root | nindent 12 }}
            {{- include "cobranch.telemetryEnv" $root | nindent 12 }}
            {{- include "cobranch.databaseEnv" $root | nindent 12 }}
            {{- with .env }}
            {{- . | nindent 12 }}
            {{- end }}
          {{- with $root.Values.blob.envFromSecret }}
          {{- if $bucket }}
          envFrom:
            - secretRef:
                name: {{ . }}
          {{- end }}
          {{- end }}
          # Only the process: a database outage makes the pods unready, never restarts them.
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 10
          # The schema is at least this version's, and the database (and the object store, for the Git server) answer.
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            periodSeconds: 5
            timeoutSeconds: 3
          resources:
            {{- toYaml $values.resources | nindent 12 }}
          securityContext:
            {{- include "cobranch.containerSecurityContext" . | nindent 12 }}
          volumeMounts:
            - name: tmp
              mountPath: /tmp
            {{- if and $bucket $root.Values.blob.volume }}
            - name: blob
              mountPath: {{ $root.Values.blob.mountPath }}
            {{- end }}
            {{- range .volumes }}
            - name: {{ .name }}
              mountPath: {{ .mountPath }}
            {{- end }}
      volumes:
        - name: tmp
          emptyDir: {}
        {{- if $bucket }}
        {{- with $root.Values.blob.volume }}
        - {{- toYaml (merge (dict "name" "blob") .) | nindent 10 }}
        {{- end }}
        {{- end }}
        {{- range .volumes }}
        - name: {{ .name }}
          emptyDir:
            {{- toYaml .emptyDir | nindent 12 }}
        {{- end }}
{{- if (hasKey . "service" | ternary .service true) }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ $name }}
  labels:
    {{- include "cobranch.labels" $component | nindent 4 }}
spec:
  selector:
    {{- include "cobranch.selectorLabels" $component | nindent 4 }}
  ports:
    - name: http
      port: 80
      targetPort: http
{{- end }}
---
apiVersion: policy/v1
kind: PodDisruptionBudget
metadata:
  name: {{ $name }}
  labels:
    {{- include "cobranch.labels" $component | nindent 4 }}
spec:
  maxUnavailable: 1
  # A drain may evict an unready pod: with one replica, the budget would hold the drain otherwise.
  unhealthyPodEvictionPolicy: AlwaysAllow
  selector:
    matchLabels:
      {{- include "cobranch.selectorLabels" $component | nindent 6 }}
{{- end -}}
