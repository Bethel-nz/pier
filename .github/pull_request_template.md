<!--
Do not post secrets, tunnel credentials, private keys, private tailnet information,
or sensitive network names. Sanitize pier.yaml, logs, command output, and
screenshots; replace private hostnames and addresses with placeholders.
-->

## Related issue

<!-- Link the issue. Use "Closes #<number>" only when this PR fully resolves it. -->

## What changed and why

<!-- Explain the problem, the change, and the resulting behavior. Keep this PR scoped to one issue. -->

## Validation actually performed

<!--
List exact commands/checks and their results, including failures. For manual
validation, give reproduction steps, expected and actual behavior, OS, Pier
version/commit, relevant Tailscale/cloudflared versions, and sanitized pier.yaml
if needed. For documentation-only changes, describe link/command/YAML checks.
Live Tailscale or Cloudflare access is not required for ordinary contributions.
-->

- Checks run and results:
- Checks not run and why:

## Safety and compatibility

<!-- Describe effects on public exposure, backend commands, route ownership, cleanup, or compatibility. Write "None" if not applicable. -->

## Checklist

- [ ] This PR links an issue and stays within its scope.
- [ ] I have reported the checks actually run and explained any checks not run.
- [ ] Unrelated routes remain untouched; no `tailscale serve reset` or `tailscale funnel reset` is introduced.
- [ ] Relevant tests and docs are updated, or I have explained why they are not needed.
- [ ] I have removed secrets, tunnel credentials, private tailnet information, and sensitive network names from this PR and its attachments.
