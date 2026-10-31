{{/*
Pages that Caddy serves, their Deployment, Service and budget: the Console's or the Back-office's. Pass a dict of the
root context, the component, its values, its image, and env: what tells its pages where each workload answers, as
COBRANCH_<name>_URL by name, an empty URL left out.
*/}}
{{- define "cobranch.site" -}}
{{- $root := .root }}
{{- $component := dict "root" $root "component" .component }}
{{- $name := printf "%s-%s" (include "cobranch.fullname" $root) .component }}
{{- $values := .values }}
apiVersion: apps/v1
kind: Deployment
metadata:
  name: {{ $name }}
  labels:
    {{- include "cobranch.labels" $component | nindent 4 }}
  annotations:
    # Argo CD rolls the pages out with the servers whose APIs they call, once the migrate Job is done.
    argocd.argoproj.io/sync-wave: "2"
spec:
  replicas: {{ $values.replicas }}
  selector:
    matchLabels:
      {{- include "cobranch.selectorLabels" $component | nindent 6 }}
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
      automountServiceAccountToken: false
      {{- with $root.Values.imagePullSecrets }}
      imagePullSecrets:
        {{- toYaml . | nindent 8 }}
      {{- end }}
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      {{- include "cobranch.spread" $component | nindent 6 }}
      {{- include "cobranch.placement" $values | nindent 6 }}
      containers:
        - name: {{ .component }}
          image: {{ include "cobranch.image" (dict "root" $root "image" .image) }}
          imagePullPolicy: {{ .image.pullPolicy }}
          ports:
            - name: http
              containerPort: 8080
          {{- $urls := dict }}
          {{- range $var, $url := .env }}
          {{- if $url }}
          {{- $_ := set $urls $var $url }}
          {{- end }}
          {{- end }}
          {{- with $urls }}
          env:
            {{- range $var, $url := . }}
            - name: COBRANCH_{{ $var }}_URL
              value: {{ $url | trimSuffix "/" | quote }}
            {{- end }}
          {{- end }}
          livenessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 10
          readinessProbe:
            httpGet:
              path: /healthz
              port: http
            periodSeconds: 5
          resources:
            {{- toYaml $values.resources | nindent 12 }}
          securityContext:
            {{- include "cobranch.containerSecurityContext" . | nindent 12 }}
          volumeMounts:
            # Caddy's state (XDG_DATA_HOME and XDG_CONFIG_HOME).
            - name: tmp
              mountPath: /tmp
      volumes:
        - name: tmp
          emptyDir: {}
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
  unhealthyPodEvictionPolicy: AlwaysAllow
  selector:
    matchLabels:
      {{- include "cobranch.selectorLabels" $component | nindent 6 }}
{{- end -}}
