#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(dirname "$script_dir")
compose="docker compose -f $script_dir/compose.yml"
checkout_url="http://localhost:${TUBICEN_CHECKOUT_1_PORT:-18080}"
checkout_2_url="http://localhost:${TUBICEN_CHECKOUT_2_PORT:-18081}"
prometheus_url="http://localhost:${TUBICEN_PROMETHEUS_PORT:-19090}"
alertmanager_url="http://localhost:${TUBICEN_ALERTMANAGER_PORT:-19093}"

cleanup() {
  if [ "${KEEP_DEMO:-0}" != "1" ]; then
    $compose --profile tools down --volumes --remove-orphans >/dev/null 2>&1 || true
  fi
}
trap cleanup EXIT INT TERM

wait_for_url() {
  url=$1
  attempts=$2
  while [ "$attempts" -gt 0 ]; do
    if curl -fsS "$url" >/dev/null 2>&1; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "timed out waiting for $url" >&2
  return 1
}

wait_for_alerts() {
  attempts=$1
  while [ "$attempts" -gt 0 ]; do
    payload=$(curl -fsS "$prometheus_url/api/v1/alerts" || true)
    if printf '%s' "$payload" | grep -q '"alertname":"CheckoutErrorBudgetBurn"' && \
       printf '%s' "$payload" | grep -q '"alertname":"CheckoutReplicaDown"' && \
       printf '%s' "$payload" | grep -q '"state":"firing"'; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "expected alerts did not reach the firing state" >&2
  curl -fsS "$prometheus_url/api/v1/alerts" >&2 || true
  return 1
}

wait_for_targets() {
  attempts=$1
  while [ "$attempts" -gt 0 ]; do
    payload=$(curl -fsS "$prometheus_url/api/v1/targets" || true)
    up_count=$(printf '%s' "$payload" | grep -o '"health":"up"' | wc -l | tr -d ' ')
    if [ "$up_count" -ge 2 ]; then
      printf '%s' "$payload"
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "Prometheus did not complete successful scrapes for both checkout replicas" >&2
  printf '%s\n' "$payload" >&2
  return 1
}

wait_for_notification() {
  attempts=$1
  while [ "$attempts" -gt 0 ]; do
    payload=$(curl -fsS "$checkout_url/notifications" || true)
    if printf '%s' "$payload" | grep -Eq '"count":[1-9][0-9]*'; then
      printf '%s' "$payload"
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 1
  done
  echo "Alertmanager did not deliver a webhook notification" >&2
  printf '%s\n' "$payload" >&2
  return 1
}

cd "$repo_dir"
echo "[1/7] Starting two checkout replicas, Prometheus, and Alertmanager"
$compose up -d --build checkout-1 checkout-2 alertmanager prometheus
wait_for_url "$checkout_url/healthz" 60
wait_for_url "$checkout_2_url/healthz" 60
wait_for_url "$prometheus_url/-/ready" 60
wait_for_url "$alertmanager_url/-/ready" 60

echo "[2/7] Checking that Prometheus sees both checkout replicas"
targets=$(wait_for_targets 30)

echo "[3/7] Degrading checkout-1 and stopping checkout-2"
curl -fsS -X POST "$checkout_url/scenario/degraded" >/dev/null
$compose stop checkout-2 >/dev/null

echo "[4/7] Waiting for Prometheus to evaluate both alerts"
wait_for_alerts 60
echo "Prometheus reports CheckoutErrorBudgetBurn and CheckoutReplicaDown as firing"

echo "[5/7] Waiting for Alertmanager to deliver a webhook notification"
notifications=$(wait_for_notification 30)
echo "Alertmanager delivered a notification to the checkout webhook"

echo "[6/7] Running the weak rule tests; this must fail the 80 percent gate"
set +e
$compose --profile tools run --rm tubicen run \
  --rules demo/prometheus/alerts.yml \
  --tests demo/tests/weak.yml \
  --threshold 80 \
  --workers 4 \
  --quiet
weak_status=$?
set -e
if [ "$weak_status" -ne 1 ]; then
  echo "weak suite returned $weak_status; expected gate failure exit code 1" >&2
  exit 1
fi

echo "[7/7] Running the stronger rule tests; this must pass the 100 percent gate"
$compose --profile tools run --rm tubicen run \
  --rules demo/prometheus/alerts.yml \
  --tests demo/tests/strong.yml \
  --threshold 100 \
  --workers 4 \
  --quiet

echo "Demo passed: live alerts fired, weak tests missed changes, and strong tests caught them."
if [ "${KEEP_DEMO:-0}" = "1" ]; then
  echo "Containers are still running because KEEP_DEMO=1."
fi
