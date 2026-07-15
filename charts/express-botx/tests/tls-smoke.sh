#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

render() { helm template test "$chart_dir" "$@"; }
render_with_notes() { helm install test "$chart_dir" --dry-run=client "$@"; }
expect_fail() {
  local name="$1"
  shift
  if render "$@" >/dev/null 2>&1; then
    echo "expected failure: $name" >&2
    exit 1
  fi
}
expect_fail_with() {
  local name="$1"
  local message="$2"
  shift 2
  local output
  if output="$(render "$@" 2>&1)"; then
    echo "expected failure: $name" >&2
    exit 1
  fi
  if ! grep -Fq "$message" <<<"$output"; then
    echo "unexpected failure: $name" >&2
    echo "$output" >&2
    exit 1
  fi
}

assert() { grep -Fq -- "$2" <<<"$1" || { echo "missing: $2" >&2; exit 1; }; }
refute() { ! grep -Fq -- "$2" <<<"$1" || { echo "unexpected: $2" >&2; exit 1; }; }
assert_literal_value() {
  grep -Eq "value: ['\"]?$2['\"]?$" <<<"$1" || {
    echo "missing literal value: $2" >&2
    exit 1
  }
}

default="$(render_with_notes)"
refute "$default" 'kind: Certificate'
refute "$default" 'EXPRESS_BOTX_SERVER_TLS_CERT'
refute "$default" 'scheme: HTTPS'
assert "$default" 'name: http'
assert "$default" 'containerPort: 8080'
assert "$default" 'targetPort: 8080'
assert "$default" 'curl http://localhost:80/healthz'
if [[ "$(grep -Fc 'port: http' <<<"$default")" -lt 2 ]]; then
  echo "both HTTP probes must use the named port" >&2
  exit 1
fi

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT
cat >"$tmp/legacy.yaml" <<'YAML'
configRaw: |
  server:
    listen: ":{{ .Values.service.targetPort }}"
extraEnv:
  - name: PORT_COPY
    value: "{{ .Values.service.targetPort }}"
YAML
legacy="$(helm template test "$chart_dir" -f "$tmp/legacy.yaml")"
assert "$legacy" 'listen: ":8080"'
assert_literal_value "$legacy" '8080'
refute "$legacy" 'EXPRESS_BOTX_SERVER_LISTEN'

cat >"$tmp/tls.yaml" <<'YAML'
containerPort: 8443
service:
  targetPort: 8443
config:
  server:
    listen: ":8443"
tls:
  enabled: true
  existingSecret: provided-tls
  reloadInterval: 15s
ingress:
  enabled: true
  hosts:
    - host: api.example.com
      paths:
        - path: /
          pathType: Prefix
YAML
secure="$(render_with_notes -f "$tmp/tls.yaml")"
refute "$secure" 'kind: Certificate'
assert "$secure" 'name: https'
assert "$secure" 'containerPort: 8443'
assert "$secure" 'targetPort: 8443'
assert "$secure" 'port: https'
assert "$secure" 'scheme: HTTPS'
assert "$secure" 'secretName: "provided-tls"'
assert "$secure" 'key: tls.crt'
assert "$secure" 'path: tls.crt'
assert "$secure" 'key: tls.key'
assert "$secure" 'path: tls.key'
assert "$secure" 'mountPath: "/etc/express-botx/tls"'
assert "$secure" 'readOnly: true'
assert "$secure" 'EXPRESS_BOTX_SERVER_TLS_CERT'
assert "$secure" 'value: "/etc/express-botx/tls/tls.crt"'
assert "$secure" 'EXPRESS_BOTX_SERVER_TLS_KEY'
assert "$secure" 'value: "/etc/express-botx/tls/tls.key"'
assert "$secure" 'EXPRESS_BOTX_SERVER_TLS_RELOAD_INTERVAL'
assert "$secure" 'value: "15s"'
refute "$secure" 'checksum/tls'
if [[ "$(grep -Fc 'EXPRESS_BOTX_SERVER_TLS_' <<<"$secure")" -ne 3 ]]; then
  echo "TLS must inject exactly three server environment variables" >&2
  exit 1
fi
if [[ "$(grep -Fc 'port: https' <<<"$secure")" -lt 2 ]]; then
  echo "both HTTPS probes must use the named port" >&2
  exit 1
fi
if [[ "$(grep -Fc 'name: https' <<<"$secure")" -lt 3 ]]; then
  echo "container, Service, and Ingress must use the HTTPS port name" >&2
  exit 1
fi

secure_notes="$(render_with_notes -f "$tmp/tls.yaml" --set ingress.enabled=false)"
assert "$secure_notes" 'kubectl port-forward svc/test-express-botx 80:8443'
assert "$secure_notes" 'curl -k https://localhost:80/healthz'

certificate="$(render --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=123 \
  --set-string 'tls.certManager.dnsNames[0]=*.example.com')"
assert "$certificate" 'kind: Certificate'
assert "$certificate" 'name: "123"'
assert "$certificate" '- "*.example.com"'
assert "$certificate" 'EXPRESS_BOTX_SERVER_TLS_RELOAD_INTERVAL'

autofill="$(render --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=issuer --set ingress.enabled=true \
  --set-string ingress.hosts[0].host=api.example.com \
  --set-string ingress.hosts[0].paths[0].path=/ \
  --set-string ingress.hosts[0].paths[0].pathType=Prefix)"
assert "$autofill" '- "api.example.com"'

named="$(render --set tls.enabled=true --set-string tls.existingSecret=provided-tls \
  --set-string service.targetPort=https)"
assert "$named" 'targetPort: https'

cat >"$tmp/raw.yaml" <<'YAML'
containerPort: 8443
service: {targetPort: 8443}
configRaw: |
  server:
    listen: ":9443"
tls: {enabled: true, existingSecret: provided-tls}
YAML
render -f "$tmp/raw.yaml" >/dev/null

cat >"$tmp/config-secret.yaml" <<'YAML'
containerPort: 8443
service: {targetPort: 8443}
existingSecret: provided-config
tls: {enabled: true, existingSecret: provided-tls}
YAML
render -f "$tmp/config-secret.yaml" >/dev/null

cat >"$tmp/env.yaml" <<'YAML'
containerPort: 8443
service: {targetPort: 8443}
extraEnv:
  - name: EXPRESS_BOTX_SERVER_LISTEN
    value: ":9443"
tls: {enabled: true, existingSecret: provided-tls}
YAML
render -f "$tmp/env.yaml" >/dev/null

server_null="$(render --set tls.enabled=true \
  --set-string tls.existingSecret=provided-tls --set-json config.server=null)"
assert "$server_null" 'kind: Deployment'
assert "$server_null" 'containerPort: 8080'

render --set tls.enabled=true --set-string tls.existingSecret=provided-tls \
  --set-string tls.reloadInterval=.5s >/dev/null

expect_fail "missing source" --set tls.enabled=true
expect_fail_with "invalid reload interval" \
  "tls.reloadInterval must be a positive Go duration" \
  --set tls.enabled=true --set-string tls.existingSecret=provided-tls \
  --set-string tls.reloadInterval=soon
expect_fail_with "zero reload interval" \
  "tls.reloadInterval must be a positive Go duration" \
  --set tls.enabled=true --set-string tls.existingSecret=provided-tls \
  --set-string tls.reloadInterval=0s
expect_fail_with "negative reload interval" \
  "tls.reloadInterval must be a positive Go duration" \
  --set tls.enabled=true --set-string tls.existingSecret=provided-tls \
  --set-string tls.reloadInterval=-1s
expect_fail_with "null TLS Secret source" \
  "tls.enabled requires tls.certManager.enabled or tls.existingSecret" \
  --set tls.enabled=true --set-json tls.existingSecret=null
expect_fail "both sources" --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=issuer \
  --set-string tls.certManager.dnsNames[0]=api.example.com \
  --set-string tls.existingSecret=provided-tls
expect_fail "missing issuer" --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.dnsNames[0]=api.example.com
expect_fail_with "null issuer" "tls.certManager.issuerRef.name is required" \
  --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-json tls.certManager.issuerRef.name=null \
  --set-string tls.certManager.dnsNames[0]=api.example.com
expect_fail_with "null issuerRef mapping" \
  "tls.certManager.issuerRef.name is required" \
  --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-json tls.certManager.issuerRef=null \
  --set-string tls.certManager.dnsNames[0]=api.example.com
expect_fail "worker" --set mode=worker --set tls.enabled=true \
  --set-string tls.existingSecret=provided-tls
expect_fail "empty DNS" --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=issuer --set ingress.enabled=true
expect_fail_with "null DNS" \
  "tls.certManager.dnsNames is required (or enable ingress with non-empty hosts)" \
  --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=issuer \
  --set-json 'tls.certManager.dnsNames=[null]'
expect_fail_with "null ingress DNS" \
  "tls.certManager.dnsNames is required (or enable ingress with non-empty hosts)" \
  --set tls.enabled=true --set tls.certManager.enabled=true \
  --set-string tls.certManager.issuerRef.name=issuer --set ingress.enabled=true \
  --set-json 'ingress.hosts=[{"host":null,"paths":[{"path":"/","pathType":"Prefix"}]}]'
expect_fail "numeric target mismatch" --set tls.enabled=true \
  --set-string tls.existingSecret=provided-tls --set containerPort=8443
expect_fail "named target mismatch" --set tls.enabled=true \
  --set-string tls.existingSecret=provided-tls --set-string service.targetPort=wrong
expect_fail "structured listen mismatch" --set tls.enabled=true \
  --set-string tls.existingSecret=provided-tls --set containerPort=8443 \
  --set service.targetPort=8443
