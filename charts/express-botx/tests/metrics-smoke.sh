#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

render() { helm template test "$chart_dir" "$@"; }
assert() { grep -Fq -- "$2" <<<"$1" || { echo "missing: $2" >&2; exit 1; }; }
refute() { ! grep -Fq -- "$2" <<<"$1" || { echo "unexpected: $2" >&2; exit 1; }; }
expect_fail_with() {
  local name="$1" message="$2"
  shift 2
  local output
  if output="$(render "$@" 2>&1)"; then
    echo "expected failure: $name" >&2
    exit 1
  fi
  grep -Fq "$message" <<<"$output" || { echo "unexpected failure: $name" >&2; echo "$output" >&2; exit 1; }
}

# Disabled by default: no port, no env, no extra objects.
out="$(render)"
refute "$out" "EXPRESS_BOTX_SERVER_METRICS_LISTEN"
refute "$out" "name: metrics"
refute "$out" "kind: ServiceMonitor"

# Enabled: env, container port and a ClusterIP Service of its own.
out="$(render --set metrics.enabled=true)"
assert "$out" "name: EXPRESS_BOTX_SERVER_METRICS_LISTEN"
assert "$out" 'value: ":9090"'
assert "$out" "containerPort: 9090"
assert "$out" "name: test-express-botx-metrics"
assert "$out" "app.kubernetes.io/component: metrics"
refute "$out" "kind: ServiceMonitor"

# The metrics Service stays ClusterIP when the main one is a LoadBalancer.
out="$(render --set metrics.enabled=true --set service.type=LoadBalancer)"
metrics_svc="$(awk '/name: test-express-botx-metrics/,/selector:/' <<<"$out")"
assert "$metrics_svc" "type: ClusterIP"
main_svc="$(awk '/^kind: Service$/{s=1} s&&/name: test-express-botx$/{p=1} p' <<<"$out" | sed -n '1,20p')"
refute "$main_svc" "name: metrics"

# Custom port and path reach the env, the app config and the ServiceMonitor.
out="$(render --set metrics.enabled=true --set metrics.port=9300 \
  --set metrics.path=/internal/metrics --set config.server.metrics.path=/internal/metrics \
  --set metrics.serviceMonitor.enabled=true \
  --set metrics.serviceMonitor.labels.release=prom --set metrics.serviceMonitor.scrapeTimeout=10s)"
assert "$out" 'value: ":9300"'
assert "$out" "kind: ServiceMonitor"
assert "$out" 'path: "/internal/metrics"'
assert "$out" "release: prom"
assert "$out" "interval: 30s"
assert "$out" "scrapeTimeout: 10s"
assert "$out" "path: /internal/metrics"

# A scrape path the app does not serve is refused.
expect_fail_with "path mismatch" "does not match the path the app serves" \
  --set metrics.enabled=true --set metrics.path=/internal/metrics
expect_fail_with "config path mismatch" "does not match the path the app serves" \
  --set metrics.enabled=true --set config.server.metrics.path=/prom

# A long release name keeps the metrics Service name valid and distinct.
long="$(printf 'a%.0s' {1..63})"
out="$(render --set metrics.enabled=true --set fullnameOverride="$long")"
name="$(awk '/component: metrics/{c=1} /^kind:/{c=0} {print}' <<<"$out" | grep -E '^  name: .*-metrics$' | head -1 | awk '{print $2}')"
[[ -n "$name" && ${#name} -le 63 ]] || { echo "metrics Service name invalid: '$name' (${#name})" >&2; exit 1; }
[[ "$name" != "$long" ]] || { echo "metrics Service name collides with the main one" >&2; exit 1; }

# serve-enqueue gets metrics too; worker refuses them.
out="$(render --set mode=serve-enqueue --set metrics.enabled=true)"
assert "$out" "name: EXPRESS_BOTX_SERVER_METRICS_LISTEN"
expect_fail_with "worker" "not supported in worker mode" --set mode=worker --set metrics.enabled=true
expect_fail_with "relative path" "metrics.path must start with /" --set metrics.enabled=true --set metrics.path=metrics

echo "metrics chart checks passed"
