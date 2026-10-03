# Compact project header

The header shared by every project page (`/deploy/[id]` and the pages under it) took two rows and
a tall identity block before the page's own content began: on Logs the request list started below
the fold. It is now one row — the mark, the address with its state and assessment beside it, one
line of facts beneath, and the command at the end — and it is not sticky.

What was cut, and where it still is:

- The `← Deployments` link: the rail's panel head already leads back to Deployments.
- The `Visit` button: the address is the link to the site.
- `deployed` before the live release's age (a rollback still says `rolled back`).
- The language and package manager after a detected framework (`Next.js · Node.js · bun` is now
  `Next.js` on the language's mark); both are read in Settings → Build.

The page's own content starts 110 pixels higher at 1440 and 115 pixels higher at 390.

| View | Before | After |
| --- | --- | --- |
| Logs, 1440 | [before](before-logs-desktop.png) | [after](after-logs-desktop.png) |
| Overview, 1440 | [before](before-overview-desktop.png) | [after](after-overview-desktop.png) |
| Run in flight, 1440 | [before](before-deploying-desktop.png) | [after](after-deploying-desktop.png) |
| Logs, 390 | [before](before-logs-phone.png) | [after](after-logs-phone.png) |

Captures use the browser fixture's showcase project (`mockProject(page, { showcase: true })`).
`frontend/tests/browser/deploy-header.spec.ts` holds the header to 64 pixels at 1280 and 1720,
keeps the state and assessment from colliding at 390, 768, 1280 and 1720, and checks long
repository and branch names without horizontal overflow.
