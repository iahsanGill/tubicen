# Contributing

Contributions that make alert tests more discriminating, execution more reliable, or reports easier to consume are welcome.

## Development setup

Install Go 1.25 or newer and `promtool`, then run:

```bash
make check
make e2e
```

`PROMTOOL=/path/to/promtool make e2e` selects a non-default binary.

## Change requirements

- Add focused tests for new behavior and failure paths.
- Keep mutations deterministic and limited to one semantic change.
- Reparse every generated PromQL expression.
- Use `promtool` as the reference evaluator; do not duplicate its semantics.
- Keep the JSON schema backward-compatible within a major release.
- Update the operator table and architecture document when behavior changes.

For a new mutation operator, include at least one test that proves the mutant is produced and one promtool fixture capable of killing it. Run `make check` before opening a pull request.

## Commit and pull request style

Prefer small commits with one reviewable purpose. A pull request should state the production defect being modeled, show the original and mutated rule form, and describe how the tests discriminate between them.
