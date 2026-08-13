#!/bin/sh
set -eu
export DOCKER_CLI_HINTS=false

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
repo_dir=$(CDPATH= cd -- "$script_dir/../.." && pwd)
compose="docker compose --ansi never -f $script_dir/compose.yml"

checkout_url="http://localhost:${TUBICEN_INCIDENT_CHECKOUT_1_PORT:-18180}"
candidate_url="http://localhost:${TUBICEN_INCIDENT_CANDIDATE_PORT:-19190}"
expected_url="http://localhost:${TUBICEN_INCIDENT_EXPECTED_PORT:-19191}"
alertmanager_url="http://localhost:${TUBICEN_INCIDENT_ALERTMANAGER_PORT:-19193}"

cleanup() {
  if [ "${KEEP_INCIDENT:-0}" != "1" ]; then
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
  echo "Could not reach $url" >&2
  return 1
}

wait_for_two_targets() {
  base_url=$1
  attempts=30
  while [ "$attempts" -gt 0 ]; do
    payload=$(curl -fsS "$base_url/api/v1/targets" || true)
    count=$(printf '%s' "$payload" | grep -o '"health":"up"' | wc -l | tr -d ' ')
    if [ "$count" -ge 2 ]; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "Prometheus did not see both checkout replicas" >&2
  return 1
}

wait_for_comparison() {
  attempts=45
  while [ "$attempts" -gt 0 ]; do
    candidate=$(curl -fsS "$candidate_url/api/v1/alerts" || true)
    expected=$(curl -fsS "$expected_url/api/v1/alerts" || true)
    if ! printf '%s' "$candidate" | grep -q '"alertname":"CheckoutReplicaDown"' && \
       printf '%s' "$expected" | grep -q '"alertname":"CheckoutReplicaDown"' && \
       printf '%s' "$expected" | grep -q '"state":"firing"'; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 2
  done
  echo "The two live rule versions did not produce the expected difference" >&2
  echo "Pull-request version: $candidate" >&2
  echo "Correct version: $expected" >&2
  return 1
}

wait_for_page() {
  attempts=30
  while [ "$attempts" -gt 0 ]; do
    notifications=$(curl -fsS "$checkout_url/notifications" || true)
    if printf '%s' "$notifications" | grep -q '"rule_version":"expected"' && \
       ! printf '%s' "$notifications" | grep -q '"rule_version":"candidate"'; then
      return 0
    fi
    attempts=$((attempts - 1))
    sleep 1
  done
  echo "The expected on-call page was not delivered" >&2
  printf '%s\n' "$notifications" >&2
  return 1
}

cd "$repo_dir"
printf '\nTUBICEN MISSED-PAGE DEMO\n'
printf '========================\n\n'
printf 'Problem: a pull request changes the checkout alert from\n'
printf '"page when any replica is down" to "page only when every replica is down."\n\n'

printf 'Preparing the same Tubicen container used by CI.\n'
$compose --profile tools build tubicen >/dev/null
printf 'Container ready.\n\n'

printf 'STEP 1 - The old test gives false confidence\n'
printf 'The old test contains one failed replica. Both versions of the rule pass it.\n'
$compose --profile tools run --rm --entrypoint promtool tubicen \
  test rules demo/incident/tests/before-candidate.yml >/dev/null
printf 'RESULT: the harmful pull-request version passes the existing test.\n\n'

printf 'STEP 2 - The pull-request check finds the missing protection\n'
printf 'Tubicen uses the repository policy and asks whether the old test can tell\n'
printf 'the healthiest-replica rule from the least-healthy-replica rule.\n'
set +e
$compose --profile tools run --rm tubicen check \
  --config demo/incident/.tubicen-pull-request.yml
old_score=$?
set -e
if [ "$old_score" -ne 1 ]; then
  echo "Expected the old test to miss the rule change" >&2
  exit 1
fi
printf 'RESULT: Tubicen reports that the existing test does not catch this change.\n\n'

printf 'STEP 3 - Reproduce the production incident\n'
printf 'Starting two checkout replicas and two copies of Prometheus.\n'
printf 'One uses the pull-request rule. One uses the correct rule.\n'
$compose up -d --build checkout-1 checkout-2 alertmanager prometheus-candidate prometheus-expected >/dev/null
wait_for_url "$checkout_url/healthz" 60
wait_for_url "$candidate_url/-/ready" 60
wait_for_url "$expected_url/-/ready" 60
wait_for_url "$alertmanager_url/-/ready" 60
wait_for_two_targets "$candidate_url"
wait_for_two_targets "$expected_url"
printf 'Both checkout replicas are running and being watched.\n'
$compose stop checkout-2 >/dev/null
printf 'checkout-2 has stopped. checkout-1 is still serving traffic.\n'
wait_for_comparison
wait_for_page
printf 'RESULT WITH PULL-REQUEST RULE: no alert; the on-call engineer is not paged.\n'
printf 'RESULT WITH CORRECT RULE: alert is firing; Alertmanager sends the page.\n\n'

printf 'STEP 4 - Add the missing test\n'
printf 'The new test describes the real failure: one healthy replica and one failed replica.\n'
set +e
$compose --profile tools run --rm --entrypoint promtool tubicen \
  test rules demo/incident/tests/after-candidate.yml >/dev/null 2>&1
candidate_new_test=$?
set -e
if [ "$candidate_new_test" -ne 1 ]; then
  echo "Expected the new baseline test to reject the pull-request rule" >&2
  exit 1
fi
printf 'RESULT: the harmful pull-request rule now fails before deployment.\n'
$compose --profile tools run --rm tubicen check \
  --config demo/incident/.tubicen-protected.yml
printf 'RESULT: the correct rule passes, and Tubicen confirms the new test catches the harmful change.\n\n'

printf 'DEMO COMPLETE\n'
printf 'The old CI check passed a rule that missed a real production page.\n'
printf 'Tubicen identified the missing case before deployment and proved the added test works.\n'
if [ "${KEEP_INCIDENT:-0}" = "1" ]; then
  printf '\nThe live comparison is still running:\n'
  printf '  Pull-request rule: %s/alerts\n' "$candidate_url"
  printf '  Correct rule:      %s/alerts\n' "$expected_url"
fi
