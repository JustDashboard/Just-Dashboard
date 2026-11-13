# frontend

The Next.js half of Just Dashboard (App Router, React, TypeScript). In an install it is the
`frontend` service of the Compose stack: a standalone server on loopback that Caddy puts beside the
API. It holds no authority of its own; every request it makes is decided again by the backend.

Bun only. Keep `bun.lock`; never add `package-lock.json` or `yarn.lock`.

## Layout

| Path | What it is |
| --- | --- |
| `src/app/` | Routes. `(dashboard)/` is the signed-in shell, one directory per sidebar section; `login/`, `welcome/` and `hub/enter/` sit outside it |
| `src/components/` | One directory per section (`components/<area>/`), `ui/` for the low-level controls, and the shared primitives at the top level. `nav.ts` is the sidebar's source |
| `src/lib/` | The API client (`api.ts`), the API's types (`types.ts`) and pure logic, each with its `*.test.js` beside it |
| `src/hooks/` | Polling, sockets, authentication and other shared state |
| `src/proxy.ts` | The request proxy: the CSP nonce and policy, and the trailing-slash rule |
| `tests/browser/` | Playwright specs (`<area>-*.spec.ts`) and the mocked APIs they run against (`*-fixture.ts`, `fixtures/`) |
| `scripts/` | Build helpers (`sync-monaco.mjs`, `sync-excalidraw.mjs`), the standalone server `bun run start` uses, and the recording scripts |
| `eslint-rules/` | The design system's lint rules, turned on in `eslint.config.mjs` |
| `public/` | Static files: the product logos, and the Monaco and Excalidraw files the sync scripts copy in |
| `Dockerfile` | The frontend image |

## Commands

Run from this directory. The checks a change needs are picked by `scripts/test-changed.sh` at the
repository root.

```bash
bun install && bun dev               # :3000, proxies /api to 127.0.0.1:8080
bun run lint && bun run build
bun test src                         # the unit tests over src/lib and src/components
bun run start --hostname 127.0.0.1 --port 43117   # the built server the browser specs open
bunx playwright test tests/browser/docker-ui.spec.ts
```

`bun run test:browser:install` installs the browser once. Rebuild and restart the server after a
source change, or the specs test the old build. The whole browser suite is never run locally.

## Where the detail is

- [`../AGENTS.md`](../AGENTS.md): the workflow, the required checks and the code style.
- [`../docs/internal/reference/finding-things.md`](../docs/internal/reference/finding-things.md):
  the route, components, handler and spec that belong to a given page.
- [`../docs/internal/frontend/design-system.md`](../docs/internal/frontend/design-system.md): the
  design system's rules, and the passes a page redesign runs.
- [`../docs/internal/frontend/shell-design.md`](../docs/internal/frontend/shell-design.md),
  [`feature-map.md`](../docs/internal/frontend/feature-map.md) and
  [`data-theming.md`](../docs/internal/frontend/data-theming.md): the shell, each route area, and
  the API and socket clients.
