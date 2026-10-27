{{/*
A server of one API: its Deployment, Service and budget. Pass a dict of the root context, the component, which names
the subcommand too, and the server's values.
*/}}
{{- define "devhub.server" -}}
{{- $root := .root }}
{{- $component := dict "root" $root "component" .component }}
{{- $name := printf "%s-%s" (include "devhub.fullname" $root) .component }}
{{- $values := .values }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $name }}
  labels:
    {{- include "devhub.labels" $component | nindent 4 }}
  annotations:
    # Argo CD rolls the servers out once the migrate Job, a wave before, is done.
    argocd.argoproj.io/sync-wave: "2"
spec:
  replicas: {{ $values.replicas }}
  selector:
    matchLabels:
      {{- include "devhub.selectorLabels" $component | nindent 6 }}
  # A new pod turns ready, its schema migrated, before an old one stops.
  strategy:
    type: RollingUpdate
    rollingUpdate:
      maxSurge: 1
      maxUnavailable: 0
  template:
    metadata:
      labels:
        {{- include "devhub.labels" $component | nindent 8 }}
      {{- with $values.podAnnotations }}
      annotations:
        {{- toYaml . | nindent 8 }}
      {{- end }}
    spec:
      serviceAccountName: {{ include "devhub.serviceAccount" $root }}
      automountServiceAccountToken: false
      {{- with $root.Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      {{- include "devhub.podSecurityContext" . | nindent 6 }}
      {{- include "devhub.spread" $component | nindent 6 }}
      {{- include "devhub.placement" $values | nindent 6 }}
      containers:
        - name: {{ .component }}
          image: {{ include "devhub.image" (dict "root" $root "image" $root.Values.images.devhub) }}
          imagePullPolicy: {{ $root.Values.images.devhub.pullPolicy }}
          args: [{{ .component }}]
          ports:
            - name: http
              containerPort: 8080
          env:
            - name: DEVHUB_HTTP_ADDR
              value: ":8080"
            - name: DEVHUB_BLOB_URL
              value: {{ $root.Values.blob.url | quote }}
            {{- with $root.Values.urls.console }}
            - name: DEVHUB_CONSOLE_URL
              value: {{ . | quote }}
            {{- end }}
            {{- include "devhub.telemetryEnv" $root | nindent 12 }}
            {{- include "devhub.databaseEnv" $root | nindent 12 }}
          {{- with $root.Values.blob.envFromSecret }}
          envFrom:
            - secretRef:
                name: {{ . }}
          {{- end }}
          # Only the process: a database outage makes the pods unready, never restarts them.
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 10
          # The schema is at least this version's, and the database and the bucket answer.
          readinessProbe:
            httpGet:
              path: /readyz
              port: http
            periodSeconds: 5
            timeoutSeconds: 3
          resources:
            {{- toYaml $values.resources | nindent 12 }}
          securityContext:
            {{- include "devhub.containerSecurityContext" . | nindent 12 }}
          volumeMounts:
            - name: tmp
              mountPath: /tmp
            {{- if $root.Values.blob.volume }}
            - name: blob
              mountPath: {{ $root.Values.blob.mountPath }}
            {{- end }}
      volumes:
        - name: tmp
          emptyDir: {}
        {{- with $root.Values.blob.volume }}
        - {{- toYaml (merge (dict "name" "blob") .) | nindent 10 }}
        {{- end }}
---
apiVersion: v1
kind: Service
metadata:
  name: {{ $name }}
  labels:
    {{- include "devhub.labels" $component | nindent 4 }}
spec:
  selector:
    {{- include "devhub.selectorLabels" $component | nindent 4 }}
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
    {{- include "devhub.labels" $component | nindent 4 }}
spec:
  maxUnavailable: 1
  # A drain may evict an unready pod: with one replica, the budget would hold the drain otherwise.
  unhealthyPodEvictionPolicy: AlwaysAllow
  selector:
    matchLabels:
      {{- include "devhub.selectorLabels" $component | nindent 6 }}
{{- end -}}
