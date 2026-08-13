# Security policy

## Reporting a vulnerability

Please use GitHub's private vulnerability reporting for this repository. Do not open a public issue containing an unpatched exploit or sensitive environment details.

Include the affected version, operating system, proof of concept, impact, and any proposed mitigation. You should receive an acknowledgement within seven days.

## Execution boundary

Tubicen launches the `promtool` executable selected by `--promtool` and supplies argument arrays without invoking a shell. Rule and test files should still be treated as untrusted inputs to both Tubicen and the selected Prometheus build. Use a pinned, trusted `promtool` release in CI.
