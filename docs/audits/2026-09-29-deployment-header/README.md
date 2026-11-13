# Deployment header layout

The shared project header keeps source, tracked branch and runtime together, with live release
timing and automatic-deployment state underneath. Readiness and assessment occupy separate lines.
Commit messages, authors and trigger details remain in the deployment history and run pages.

`frontend/tests/browser/deploy-header.spec.ts` checks the two rows and stacked health readings at
390, 768, 1280 and 1720 pixels, keeps detailed provenance visible in the history, and checks long
repository and branch names on the Runtime header without horizontal overflow.

| Width | Before | After |
| --- | --- | --- |
| 1280 | [Desktop before](https://raw.githubusercontent.com/JustDashboard/Just-Dashboard/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-header/before-desktop.png) | [Desktop after](https://raw.githubusercontent.com/JustDashboard/Just-Dashboard/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-header/after-desktop.png) |
| 390 | [Phone before](https://raw.githubusercontent.com/JustDashboard/Just-Dashboard/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-header/before-phone.png) | [Phone after](https://raw.githubusercontent.com/JustDashboard/Just-Dashboard/397e711c0c5c01e4c0c32b9ed34061f3198e4dc4/docs/audits/2026-09-29-deployment-header/after-phone.png) |

Captures use the same mocked completed deployment and healthy assessment. Local validation:
`bun run build` and `JD_BROWSER_BASE_URL=http://127.0.0.1:43121 scripts/test-changed.sh patch/0.7.1`.
