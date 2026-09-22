# Deployment creation audit and implementation plan

Scope: `/deploy/new`, its revisioned draft API, built-in templates, automatic framework recipes,
public addresses and managed/external database connections. Base: `patch/0.7.0` at `fd4e1e2`.
Worktree branch: `fix/deploy-new-audit`. This is a functional and interaction audit of the existing
flow register, not a visual redesign or a claim that every application framework can be autodetected.

## Plan

1. Trace each source choice through inspection, configuration, preflight, commit and enqueue.
   Reproduce template switching and delayed-response behavior in browser fixtures.
2. Stabilize template selection, retain per-template edits, reject obsolete async results, and
   serialize source reinspection. Save edited intent alongside configuration.
3. Repair address and database edge cases: retain access protection while editing a domain, disable
   all public routes together, ignore abandoned connection requests, and allocate unused default
   database names without adopting existing data.
4. Validate every template's normalized plan and required credentials. Route externally supplied
   secrets through encrypted variables and database references. Check framework parsing against
   supported manifest syntax and keep unsupported modes explicit.
5. Run local frontend/backend gates, browser regressions, the entire offered-template Docker sweep,
   framework build fixtures and applicable database/deployment live and race checks. Inspect the
   flow at desktop and narrow widths. Record failures and validation boundaries accurately.
6. Review the complete diff and affected documentation, commit, push the isolated branch and open
   a pull request targeting `patch/0.7.0`.

## Confirmed findings

| Area | Failure | Resolution |
| --- | --- | --- |
| Template selection | Details disappear during each fetch, changing the grid width twice; loading and errors are invisible. | Stable detail surface with explicit loading and retry. |
| Template draft | Selecting again or switching templates drops entered values. | Preserve independent template edits and ignore redundant selection. |
| Narrow template layout | Selection leaves settings below the complete catalogue, making a tap appear ineffective. | Bring selected settings into view and provide an explicit return to template choices. |
| Source deep links | Reloading after import still applies the original source query, hiding the saved Configure screen. | Consume source-selection parameters once inspection succeeds. |
| Flow navigation | Source import and step changes can leave the next question above the viewport. | Move focus and scroll to the current question, respecting reduced motion. |
| Async source work | An inspection can finish after the operator selects another source. | Ignore obsolete results and serialize dependent writes. |
| Git branch | Typing can trigger overlapping inspection and draft revision updates. | Apply deliberate branch changes, preserve in-progress editing. |
| Intent | Name/type edits are displayed but not saved with the draft configuration. | Persist intent before dependent configuration and preflight. |
| Public routes | Disabling publication with aliases promotes an alias instead of disabling publication. | Clear every requested public route. |
| Route protection | Editing the hostname or HTTPS toggle drops visitor credentials. | Preserve the complete domain record while editing individual fields. |
| Database selection | A URL request can connect a database after its sheet was abandoned. | Abort and ignore abandoned requests. |
| Database naming | A second default database of the same engine collides with the first. | Allocate an unused default name; keep explicit-name refusal. |
| Mongo Express | An authenticated MongoDB URL is an ordinary persisted template input. | Request it through the encrypted variable/database flow. |
| Deno | JSONC regex parsing mishandles inline comments and comment markers in strings. | Parse comments without modifying string contents. |
| Review | Configuration-only comparison misses intent and variable changes after preflight. | Compare the complete pending plan and clear obsolete acknowledgements. |
| Draft secrets | Required credentials cannot pass preflight before the old post-commit import; interruption leaves an incomplete project. | Encrypt inputs in the draft and transfer them atomically with the first plan. Resume using masked key names. |
| Browser persistence | Visitor passwords, pasted Compose documents and sensitive Git URLs can survive in session storage. | Keep raw inputs in memory; persist sanitized state and server-side credential hashes only. |
| Generated credentials | A supplied override can erase its generator/default, leaving no fallback after removal. | Preserve declared defaults; sealed overrides take precedence until explicitly removed. |
| Empty overrides | Reload drops an explicitly empty saved value and restores a generator or public default. | Retain every staged key, including deliberately empty values; required-value checks still block empty credentials. |
| Redetection | New source detection can erase extra staged-variable metadata or edited variable scopes. | Retain staged keys and merge updated detection defaults with operator overrides. |
| Template web tools | Tool profiles choose a service strategy inconsistent with the web-only blue/green preflight rules. | Map HTTP tools to the web profile. |
| Template retirement | Direct draft API requests can deploy catalogue entries hidden as retired. | Reject new retired sources while preserving existing deployments. |
| Template domains | Changing the public hostname or HTTPS leaves generated application URLs pointing to the original address. | Track domain-derived defaults, update them with the primary address and preserve manual overrides. |
| Database adoption | Matching only the server endpoint can replace a connection to a different logical database or account. | Match driver, host, port, database and user; refresh only the password and preserve transport options. |
| Database resume | Switching source tabs after container creation loses retry state and can create another database. | Retain the created container identity and resume connection setup. |
| Database availability | An initial engine-options failure has no recovery control. | Expose a retry without losing the form. |
| Rust | Multiple Cargo binaries ignore `package.default-run`. | Select the manifest's intended executable. |
| .NET | Multi-target projects choose unsupported/platform targets or publish ambiguously. | Select the newest supported portable target and use the same framework for restore and publish. |

## How the pieces fit together

Source selection creates a revisioned, account-owned draft. Inspection resolves the source and supplies
detected build/runtime defaults; the form retains deliberate overrides. Template rendering supplies
reviewed image pins, volumes, checks, public URL defaults and password generators. Ordinary template
inputs are validated before inspection; private connection strings enter through encrypted variables.

Configuration saves the project intent, normalized plan and encrypted environment inputs. Preflight
checks that same revision, including required inputs, references, routing, resources and acknowledged
warnings. Commit creates the project, environment, initial immutable plan and sealed variables in one
transaction. Save only stops there; Deploy enqueues the runtime engine against that committed plan.

The database step can create a managed database or link an existing connection using a database
reference. Private managed-network aliases survive replacement containers. External URLs are supplied
as encrypted variables, including their provider-required connection options. Advanced configuration
continues to expose variable scopes, build/start commands, ports, checks, resources and dependencies.
Unsupported automatic detection retains the explicit Dockerfile, image and Compose paths.

## Validation evidence

The [template matrix](template-matrix.md) records all 62 shipped definitions: 55 deployable entries,
two game-server previews and five retired entries. All 55 deployable entries passed the real Docker
startup/readiness sweep. All five managed database engines passed real connection, replacement and
reconnection tests. All 22 framework fixtures built and served their expected responses.

| Check | Evidence |
| --- | --- |
| Catalogue | All 55 deployable templates started and passed their own checks; final Adminer and Mongo Express rerun also passed. |
| Framework recipes | 22 real builds passed: Vite, Next.js, both Svelte adapters, HTML, Containerfile, Go, Astro, Nuxt, React Router, FastAPI, Flask, Django, Rust, Maven, Gradle, .NET, Deno, Laravel, PHP, Streamlit and Gradio. |
| Artifact adapters | All six C4 live cases passed, including secret-layer checks and failed-build preservation. |
| Managed databases | All five engines passed application connection and replacement/reconnection checks; Compose network merge passed. |
| Public API acceptance | 29/29 checks passed, including required encrypted input on the first runtime, generated PostgreSQL password authentication, incorrect-password rejection and paused backup automation. |
| Race detection | Full deploy/API/proxy/backups/store gate passed; focused final staging tests passed after fallback and explicit-empty-override corrections. |
| Backend gate | `go build ./...`, `go vet ./...` and the full `go test -p 1 ./...` passed. |
| Frontend gate | Lint, TypeScript, 131 pure tests (350 assertions) and the production build passed. Full browser suite: 393 passed; 16 opt-in security screenshot cases skipped. The deployment regressions and design-system checks passed. |
| Visual review | 390, 1280 and 1720 pixel widths checked; stable template details, no horizontal overflow, current question visible and focused with normal and reduced motion. |

The first broad backend run's live Docker disk-usage comparison overlapped the framework builds and
observed different image/cache inventories between snapshots. The complete gate passed unchanged after
the build fixtures finished. Browser checks use the freshly built worktree server on port 43118, separate
from the operator's existing frontend.

Documentation review covered the complete diff against `AGENTS.md`, `README.md`, `CONTRIBUTING.md` and
`docs/internal/`; affected behavior and verification guides are updated in this change. No CI or
dependency changes are included, and the draft environment column uses the additive migration path.

Startup/readiness does not prove every upstream application's first-account workflow or every external
provider integration. Public DNS ownership and certificate issuance depend on the operator's domain;
no live external Supabase account or public-domain credential was supplied for this audit. Preview and
retired entries remain explicitly unavailable for new deployments. Automatic recipes cover the
documented framework shapes, not every possible repository layout or language ecosystem.
