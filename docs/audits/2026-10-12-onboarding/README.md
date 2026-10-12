# The first-run question

The operator asked for an onboarding step after sign-in that learns what an account uses the
dashboard for and gives it only the pages it needs. They also warned that many pages have buttons
that lead into other sections, and those sections might be among the ones hidden. This change
starts `patch/0.7.3` from `main` at `d796f708` (0.7.2).

## What "only the pages it needs" means here

The first account on a server is its installer, already root through the terminal. Taking pages
away from that account does not protect anything; it only gets in the way. And sections are deeply
linked:

- About 150 links cross from one section into another, all written as plain addresses.
- Docker, Files, Deployments, the firewall, Processes and Proxy each have more than ten links
  coming in.
- Logs, Files, Processes and the terminal are embedded in other sections' pages.
- The server sends its own links too: a deployment's findings point into the proxy, firewall,
  backups and Docker.

Blocking a hidden section would turn every missed link into a dead end. So the answer narrows what
the dashboard *shows*, never what an account may *do*:

| A hidden section loses | It keeps |
| --- | --- |
| Its row in the sidebar | Every route; capability checks are unchanged |
| Its tiles on the Overview, and the requests behind them | Every link into it from another page |
| Its pages in search's empty list | Its pages in search as soon as a word is typed |

When a link lands in a hidden section, the sidebar draws that section anyway so it can say where you
are. It adds one line, *"Proxy & TLS is hidden from your sidebar. Show it"*, which puts it back.

## The flow

`/welcome` is a flow page (design system §16) outside the shell, because the sidebar is what it
decides. The shell sends an account there after sign-in, two-factor and any forced password change,
until the account answers.

1. **What will you use this server for?** Six purposes, any number of them. Each card lists the
   sections it keeps, with the sidebar's own glyphs. On a server that already has deployment projects
   or saved databases, those purposes start chosen, and the panel's head says how many there are.
2. **Which sections should your sidebar show?** The purposes fill in the switches, and the switches
   decide. Beside them is a preview of the sidebar drawn from the same registry and rule the real one
   uses. The account is offered only sections its role can see.
3. **Your sidebar is ready.** What hiding does and does not do, and how to change it.

*Show me everything* on the first screen hides nothing, and it also ends the question. Account →
Workspace offers the same switches and preview afterwards, plus *Ask me again*.

The answer is stored on the account, not in the browser, so it follows the person:

- `users.needs_onboarding` and `users.hidden_sections` are additive columns.
- Accounts that predate them are treated as answered, with nothing hidden.
- `PUT /account/workspace` writes the answer. It is session-only and audited as
  `account.workspace.update`.

## Evidence

| What | Where |
| --- | --- |
| The whole walk: sign-in, the three steps, the dashboard, a project's domain opening the hidden proxy, search, Workspace | [Recording (mp4)](onboarding.mp4) · [GIF](onboarding.gif) |
| Step 1 at 1280 / 1720 / 390 | [1280](purpose-1280.png), [1720](purpose-1720.png), [390](purpose-390.png) |
| Step 2, switches beside the preview, at 1280 / 1720 | [1280](sidebar-1280.png), [1720](sidebar-1720.png) |
| Step 3 at 1280 / 390 | [1280](ready-1280.png), [390](ready-390.png) |
| The Overview with Proxy, Network, Security and the rest hidden | [1280](overview-1280.png) |
| A deployment's domain opening the hidden proxy, the rail's note | [1280](hidden-section-1280.png), [1720](hidden-section-1720.png) |
| Account → Workspace | [1280](workspace-1280.png), [1720](workspace-1720.png) |

`frontend/scripts/record-onboarding.ts` records the walk and the 1280 screenshots against the
browser fixture (`tests/browser/onboarding-fixture.ts`). That fixture layers three existing mocks:
the deployment project, the proxy showcase, and the host with the Overview's reads. The mp4 and the
GIF are ffmpeg conversions of the script's `onboarding.webm`.
