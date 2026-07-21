{{/*
Non-secret REGISTRYD_* environment shared by the bootstrap init container and the
daemon. Secrets come from the referenced Secret via envFrom.
*/}}
{{- define "registryd.env" -}}
- name: REGISTRYD_BASE_URL
  value: {{ .Values.config.baseURL | quote }}
- name: REGISTRYD_SUPER_ADMINS
  value: {{ .Values.config.superAdmins | quote }}
- name: REGISTRYD_PG_ADDR
  value: {{ .Values.postgres.addr | quote }}
- name: REGISTRYD_PG_USERNAME
  value: {{ .Values.postgres.username | quote }}
- name: REGISTRYD_PG_DATABASE
  value: {{ .Values.postgres.database | quote }}
- name: REGISTRYD_API_ADDR
  value: "0.0.0.0:8080"
- name: REGISTRYD_METRICS_ADDR
  value: "0.0.0.0:8081"
- name: REGISTRYD_API_RATE_LIMIT_RPM
  value: {{ .Values.config.api.rateLimitRpm | quote }}
- name: REGISTRYD_API_RATE_LIMIT_BURST
  value: {{ .Values.config.api.rateLimitBurst | quote }}
{{- end -}}
