# Shared database creation

The Database source on `/deploy/new` now reuses the full creation flow from `/databases/new`: the
shelved engine catalogue, settings panel, version and account inputs, and local/public exposure choice.
Both pages use the app deployment's sweeping stage segments, marks, shimmer and moving panel border.
Progress follows provision, adopt and a fresh ping; Deploy then reads the masked connection string.
Retries continue the container that was already created.

Screenshots use mocked API responses and production builds. Before is commit `2fe48306`; after is this
change. Settings and progress were inspected on both pages at 1280, 1720 and 390 pixels wide. These are
representative captures; the animation itself is demonstrated by [the recording](creation.webm).

| View | Before | After |
| --- | --- | --- |
| Deploy's creation form | [Settings](before-deploy-1280.png) | [Settings](after-deploy-1280.png) |
| Databases startup | [Progress](before-databases-progress-1280.png) | [Progress](after-databases-progress-1280.png) |
| Deploy startup | — | [Desktop](after-deploy-progress-1280.png), [Phone](after-deploy-progress-390.png) |

Verification includes `bun run build`, `scripts/test-changed.sh patch/0.7.1`, and browser coverage in
`database-creation.spec.ts` for both pages: full form submissions, API-driven stages, a failed first-boot
ping, readiness timeout/retry without duplicate provisioning, and reduced motion at phone width.
Existing Deploy regressions also cover source-switch resumption and retrying a failed URL read.

The final changed-file run passed formatting, ESLint, TypeScript and all 2,812 unit tests. It selected
294 browser cases: 290 passed in the parallel run and four existing inventory, draft-reload and
automation cases hit timing limits on the shared machine. All four passed when rerun with
`bunx playwright test <spec> --grep <case> --workers=1` against the same production build, with no
product changes or timeout increases. All six new creation cases passed in the parallel run.
