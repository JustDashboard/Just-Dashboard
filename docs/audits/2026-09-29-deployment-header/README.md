# Deployment header layout

The shared project header keeps source, tracked branch and runtime together, with live release
timing and automatic-deployment state underneath. Readiness and assessment occupy separate lines.
Commit messages, authors and trigger details remain in the deployment history and run pages.

`frontend/tests/browser/deploy-header.spec.ts` checks the two rows and stacked health readings at
390, 768, 1280 and 1720 pixels, keeps detailed provenance visible in the history, and checks long
repository and branch names on the Runtime header without horizontal overflow.

| Width | Before | After |
| --- | --- | --- |
| 1280 | [Desktop before](before-desktop.png) | [Desktop after](after-desktop.png) |
| 390 | [Phone before](before-phone.png) | [Phone after](after-phone.png) |

Captures use the same mocked completed deployment and healthy assessment. Local validation:
`bun run build` and `JD_BROWSER_BASE_URL=http://127.0.0.1:43121 scripts/test-changed.sh patch/0.7.1`.
