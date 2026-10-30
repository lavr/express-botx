#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

all=(
  --set metrics.enabled=true
  --set metrics.serviceMonitor.enabled=true
  --set metrics.grafanaDashboard.enabled=true
  --set ingress.enabled=true
  --set 'ingress.hosts[0].host=bot.example.com'
  --set 'ingress.hosts[0].paths[0].path=/'
  --set 'ingress.hosts[0].paths[0].pathType=Prefix'
  --set autoscaling.enabled=true
  --set tls.enabled=true
  --set tls.certManager.enabled=true
  --set tls.certManager.issuerRef.name=ca
)

render() { helm template test "$chart_dir" "${all[@]}" "$@"; }
fail() { echo "$1" >&2; exit 1; }

common='commonLabels:
  team: "{{ .Values.global.team }}"
  app.kubernetes.io/name: other
commonAnnotations:
  owner: "{{ .Values.global.team }}"
global:
  team: bots
metrics:
  service:
    annotations:
      owner: metrics
podLabels:
  pod: "{{ .Values.global.team }}"'

out="$(render -f <(echo "$common"))"
kinds="$(grep -c '^kind:' <<<"$out")"
[ "$(grep -c '^    team: bots$' <<<"$out")" -eq "$kinds" ] || fail "commonLabels missing on some resources"
[ "$(grep -Ec '^    owner: (bots|metrics)$' <<<"$out")" -eq "$kinds" ] || fail "commonAnnotations missing on some resources"
grep -Fq '    owner: metrics' <<<"$out" || fail "resource annotations must win over commonAnnotations"
! grep -Fq 'app.kubernetes.io/name: other' <<<"$out" || fail "commonLabels must not override chart labels"
grep -Fq '        team: bots' <<<"$out" || fail "commonLabels missing on the pod template"
grep -Fq '        pod: bots' <<<"$out" || fail "podLabels must go through tpl"
selector="$(awk '/^kind: Deployment$/{d=1} d&&/matchLabels:/{m=1;next} m&&/^  template:/{exit} m' <<<"$out")"
! grep -Fq 'team:' <<<"$selector" || fail "commonLabels must not reach the selector"

out="$(render)"
! grep -Fq 'annotations: {}' <<<"$out" || fail "empty annotations must not be rendered"
