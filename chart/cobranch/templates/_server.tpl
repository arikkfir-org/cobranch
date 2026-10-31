{{/*
A server of one API: its Deployment, Service and budget. Pass a dict of the root context, the component, which names
the subcommand too, and the server's values.
*/}}
{{- define "cobranch.server" -}}
{{- $root := .root }}
{{- $component := dict "root" $root "component" .component }}
{{- $name := printf "%s-%s" (include "cobranch.fullname" $root) .component }}
{{- $values := .values }}
{{- $api := ne .component "auth-server" }}
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
  replicas: {{ $values.replicas }}
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
      {{- if $api }}
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
          env:
            - name: COBRANCH_HTTP_ADDR
              value: ":8080"
            {{- if $api }}
            - name: COBRANCH_BLOB_URL
              value: {{ $root.Values.blob.url | quote }}
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
          {{- with $root.Values.blob.envFromSecret }}
          {{- if $api }}
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
          # The schema is at least this version's, and the database (and, for an API, the bucket) answer.
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
            {{- if and $api $root.Values.blob.volume }}
            - name: blob
              mountPath: {{ $root.Values.blob.mountPath }}
            {{- end }}
      volumes:
        - name: tmp
          emptyDir: {}
        {{- if $api }}
        {{- with $root.Values.blob.volume }}
        - {{- toYaml (merge (dict "name" "blob") .) | nindent 10 }}
        {{- end }}
        {{- end }}
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
