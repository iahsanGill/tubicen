# The problem Tubicen prevents

This is the shortest useful description of the project:

> A change to an alert rule can pass its existing tests and still stop the on-call engineer from being paged during a real failure. Tubicen checks whether the tests would catch that kind of change before it is deployed.

## The incident

The checkout service runs two replicas so that one can fail while the other continues serving requests.

The correct rule says:

> Page the payments team when **any** checkout replica is down.

An engineer opens a pull request that changes one word in the rule's meaning:

> Page the payments team only when **every** checkout replica is down.

The change is easy to miss in a code review:

```diff
- min by (service) (up{service="checkout"}) == 0
+ max by (service) (up{service="checkout"}) == 0
```

The old test uses one failed replica. With only one replica in the test data, “any” and “every” give the same answer. The correct rule passes. The harmful rule also passes. CI is green.

In production, the service has two replicas. When one fails and one stays healthy:

- half of the checkout capacity is gone;
- the correct rule sends a page;
- the pull-request rule stays quiet;
- the on-call engineer does not know about the failure.

## What Tubicen adds

The repository's required Tubicen check reads the rule and asks a practical question the old test never asked:

> If “any replica” were accidentally changed to “every replica,” would this test fail?

It makes that change in a temporary copy, runs the existing test, and finds that the test still passes. The command returns a failing exit code, so CI can block the merge. The report points to the exact unprotected distinction between `min` and `max`.

The engineer then adds one missing case: one replica is healthy and one is down. That case rejects the harmful rule. Tubicen reruns its check and confirms that the added test now protects the rule.

## Run the complete incident

```bash
./demo/incident/run.sh
```

The script does not fake the outcome. It starts:

- two working checkout containers;
- one Prometheus with the pull-request rule;
- one Prometheus with the correct rule;
- Alertmanager and a real webhook receiver;
- Tubicen in the same form used by CI.

The captured run is included in the main [project README](../../README.md#captured-from-the-running-demo): the blocked repository check, the quiet pull-request rule, the firing correct rule, the Alertmanager route, and the received webhook payload are shown separately.

It proves, in order:

1. The harmful rule passes the old test.
2. Tubicen reports the exact rule change the old test misses.
3. Both Prometheus servers watch the same two live replicas.
4. One replica is stopped.
5. The pull-request rule produces no alert.
6. The correct rule fires and Alertmanager sends the page.
7. The new test rejects the harmful rule before deployment.
8. The same repository check confirms that the new test catches the problem.

The demo policies are ordinary checked-in files:

- `.tubicen-pull-request.yml` represents the harmful pull request and old tests;
- `.tubicen-protected.yml` represents the corrected rule and added test.

There is no Tubicen server in the stack. The Tubicen container starts for the CI check, returns an exit code, and stops.

Set `KEEP_INCIDENT=1` to leave the live comparison running:

```bash
KEEP_INCIDENT=1 ./demo/incident/run.sh
```

Then compare:

- Pull-request rule: <http://localhost:19190/alerts>
- Correct rule: <http://localhost:19191/alerts>
- Delivered pages: <http://localhost:18180/notifications>

Clean up with:

```bash
docker compose -f demo/incident/compose.yml --profile tools down --volumes
```

## What this demonstrates—and what it does not

This demo does not claim that Tubicen predicts every possible outage. It demonstrates a narrower, useful guarantee:

- The original rule works for the cases in the test file.
- Tubicen checks whether those cases can tell the intended rule apart from realistic wrong versions.
- A missed change becomes a specific test to add, not a vague coverage percentage.
- The same test can then block the harmful rule in CI.

Prometheus still evaluates live metrics. Alertmanager still sends pages. Tubicen belongs before deployment, where it checks the tests that protect those rules.
