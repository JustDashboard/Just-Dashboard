# Deployment console toolbar

- `before.png` is the operator's supplied screenshot of the toolbar to simplify.
- `after-desktop.png` and `after-mobile.png` show the production frontend at 1440px and 390px,
  using the deployment project's browser fixture with two running containers.

The toolbar keeps only Terminal actions and fullscreen. Shell, Run as, container, search, snippets
and terminal settings controls are removed. The default shell uses the image's user; another
container remains reachable through its Runtime page's Console action.

The captures come from the console scenario in `frontend/tests/browser/deploy-project.spec.ts`.
Validation uses `bun run build` and `scripts/test-changed.sh patch/0.7.1` against that build.
