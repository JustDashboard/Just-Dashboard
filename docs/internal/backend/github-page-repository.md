# GitHub page: what is inside a repository

The repository detail view of the GitHub page (`/github/[owner]/[repo]`) reads and changes a
repository's branches, commits, pull requests, issues, workflows and collaborators through GitHub's
REST API, always as the person who connected the account (their user access token, never the App's
own identity). The App, the connections, the token lifecycle, the repository list and the
repository's own settings are in [github-page.md](github-page.md); this document is the part under
`/api/v1/github/connections/{connection}/repos/{owner}/{repo}/…`.

Code: `backend/internal/githubrepos/{branches,commits,pulls,issues,actions,collaborators}.go`
(methods on `RepoAccess`, shared helpers in `resources.go`) and
`backend/internal/api/handlers_githubrepos_resources.go` (routes). The JSON shapes are
`frontend/src/lib/github-types.ts` (`GitHubBranch` to `GitHubCollaboratorResult`).

## The order of every request

1. **Session and tier** (route middleware): reads need a session; recoverable changes need
   `service.control`; deleting a branch, removing a collaborator and withdrawing an invitation are in
   `s.destructive` (ordinary confirmation, the destructive rate budget). Readonly accounts are refused
   every change, limited accounts the destructive ones, with no request to GitHub.
2. **Values** checked before anything is resolved: numbers and ids are positive integers
   (`ParsePositive`), branch names follow git's ref rules for one branch (`ValidBranchName`: no
   `refs/` prefix, nothing `check-ref-format` refuses, at most 255 bytes) and only reach a URL through
   `EscapeRef`, usernames are `ValidOwner`, a merge names a full 40-hex head. A refusal is
   `InvalidError` → **400 `bad_request`** (`githubResourceErr`, which then falls through to
   `githubPageErr`).
3. **Access**: `s.githubRepo(r)` resolves the account's own connection and the repository with its
   abilities (`ComputeAbilities`: the person's role × the App's grant on the installation × archived).
4. **Ability**: `access.Require(can.X, "x")` refuses with the ability's reason → **403
   `github_permission`**, again before GitHub is asked.
5. **GitHub** answers; its refusals are mapped once by `githubPageErr` (401 → `github_reconnect`,
   rate limits → 429 with `Retry-After`, 405/409 → `github_conflict`, 422 → `github_validation`, …).
6. Every change is audited (`github.<noun>.<verb>`, target `<owner>/<name>`, detail with `ok` and
   the number, branch, username, role or method) and, on success, `s.forgetGitHubRepo(access)` drops
   the cached access and repository list. **A title, a body, a commit message or an input value is
   never an audit detail**: an issue update records which fields changed, a dispatch how many inputs.

## Areas

Levels below are GitHub's: the App permission the call needs (the manifest grants all of them), and
the repository role GitHub requires of the person.

| Area | GitHub endpoints | App permission | Role | Unreadable is |
| --- | --- | --- | --- | --- |
| Branches | `GET /branches?per_page=100` | metadata: read | read | an error |
| Protection | `GET /branches/{b}`, `GET /branches/{b}/protection` | administration: read | admin | `protectionReadable: false` + sentence |
| Rules | `GET /rules/branches/{b}` (walk), `GET /rulesets?includes_parents=true` for names | metadata: read | read | `rulesReadable: false` + sentence; names best effort |
| Create / delete branch | `GET /git/ref/heads/{from}`, `POST /git/refs`, `DELETE /git/refs/heads/{b}` | contents: write | write (`push`) | — |
| Commits | `GET /commits?per_page=30&sha=` | contents: read | read | an empty repository (409) is an empty page |
| Pull requests | `GET /pulls?state=`, `GET /pulls/{n}`, `GET /pulls/{n}/reviews` (walk) | pull_requests: read | read | an error |
| Checks | `GET /commits/{sha}/check-runs`, `GET /commits/{sha}/status` | checks: read, statuses: read | read | `checksReadable: false` + sentence; the half that answered is kept |
| Merge methods | `GET /repos/{o}/{r}` `allow_*` | metadata: read | push (GitHub states them only then) | a missing flag offers the method; GitHub decides |
| Merge | `PUT /pulls/{n}/merge` | contents: write | write (`merge`) | — |
| Close / reopen | `PATCH /pulls/{n}` `{state}` | pull_requests: write | triage (`pulls`) | — |
| Issues | `GET /issues?state=&labels=` (pull requests dropped), `GET /issues/{n}` | issues: read | read | an error |
| Open issue | `POST /issues` | issues: write | read (`createIssues`); labels need triage (`issues`) | — |
| Edit issue | `GET` then `PATCH /issues/{n}` (only sent fields) | issues: write | triage for state/labels; title/body left to GitHub | — |
| Labels | `GET /labels?per_page=100` (walk) | issues: read | read | an error |
| Workflows | `GET /actions/workflows` (walk), `GET /contents/{path}?ref=<default>` | actions: read, contents: read | read | `readable: false` + sentence |
| Runs | `GET /actions/runs` or `/actions/workflows/{id}/runs`, `branch` passed | actions: read | read | an error |
| Dispatch | `GET /actions/workflows/{id}`, `GET /contents/{path}?ref=<ref>`, `POST …/dispatches` | actions: write | write (`actions`) | — |
| Re-run / cancel | `POST /actions/runs/{id}/rerun` or `/rerun-failed-jobs`, `POST …/cancel` | actions: write | write (`actions`) | a finished run's cancel is 409 |
| Collaborators | `GET /collaborators?affiliation=all` (walk) | metadata: read | write (GitHub's rule) | `readable: false` + sentence; invitations then not asked |
| Invitations | `GET /invitations` (walk) | administration: read | admin | `invitationsReadable: false` |
| Set / remove / withdraw | `PUT`/`DELETE /collaborators/{u}`, `DELETE /invitations/{id}` | administration: write | admin (`administer`) | — |

Paths above are relative to `/repos/{owner}/{repo}`. "Walk" follows the Link header up to
`MaxPages` (30); a page view uses one page and its `hasMore` from the Link header.

Notes per area:

- **Branches.** The default branch is marked from `Repository.DefaultBranch` and **refused for
  deletion with 400** before the confirmation reaches GitHub. Creating from a name reads that branch's
  tip first; from a 40-hex sha asks nothing first. Protection: `GET /branches/{b}` says whether
  classic protection is on (anyone may read it); the protection detail needs admin, and GitHub's 404
  there means "not an admin" once the branch said it is protected. Rules are summarised per type in
  words ("Pull request with 2 approvals, code owner review", "Required checks: build, test", "No force
  pushes", "Linear history", "Commit messages must start with …"); an unknown type is shown by its
  name. A branch with rules counts as protected.
- **Pull requests.** GitHub's list has no merged filter: `closed` and `merged` both read a page of
  closed ones and keep their half (`merged_at` set or not), so a page may be short; `hasMore` still
  comes from Link. Reviews keep each reviewer's latest verdict; a later COMMENTED review does not
  withdraw an approval or a change request, as on GitHub. Check runs and commit statuses fold into one
  list in the check run's words (a status's `error` is a failed check, `pending` is status
  `pending`), failures first; a details address is kept only if it is http(s).
- **Merge's head guard.** `sha` is required (40-hex, checked in the handler before the repository is
  resolved, and again in the package) and sent to GitHub, which refuses the merge if the head moved
  (409); a pull request GitHub will not merge answers 405. Both are **409 `github_conflict`** with
  GitHub's message. Nobody merges commits they did not see.
- **Issues.** A pull request's number is 404 on the issue routes (the issue endpoint would edit the
  pull request's title too). Title is required and at most 256 characters, body at most 65,536,
  labels at most 100 of at most 50 characters; a label filter cannot contain a comma (GitHub's list
  syntax). `labels: []` clears labels; leaving `labels` out leaves them.

## Dispatch detection and its limits

`GET …/actions/workflows` reads each **active** workflow's file **on the default branch** (contents
API, base64, at most 1 MiB, four at once) and parses it with `gopkg.in/yaml.v3` as a node tree
(`ParseDispatch`). `on` may be one event, a list, or a map; a key written `true` (what a YAML 1.1
reader makes of a bare `on`) is accepted too. `workflow_dispatch.inputs` gives each input's name,
description, required, default (as text), type (`string`, `boolean`, `choice`, `number`,
`environment`; anything else is `string`) and options. More than 25 inputs, a file that does not
parse, or one that cannot be read is `dispatch.available: false` with the reason. A disabled workflow
and GitHub's own dynamic workflows (a path outside `.github/workflows/`, e.g. Dependabot) are not
dispatchable and their files are not read. Parsed results are cached by repository + path + blob
sha in a bounded map (512 entries): the contents are still asked for (their sha is the key), the
parse is not repeated.

Limits, deliberately:

- **Inputs come only from the default branch's file** in the list. A branch that adds or changes
  inputs shows them once merged.
- **A dispatch is checked again** against the workflow and the file **at the ref it runs on**, never
  against what the page was shown: no dispatch trigger, a required input without a value or default,
  an undeclared input, a choice value outside its options, a boolean that is not `true`/`false` or a
  number that does not parse → 400 before GitHub is asked. At most 25 inputs of at most 65,535
  characters each.
- **A reusable workflow's `workflow_call` is not a dispatch** and is not offered.
- The dispatch sends `return_run_details: true`; GitHub answers 200 with the run it started
  (`runId`, `htmlUrl`) where it supports that, or 204 with nothing, which the page shows as "started"
  without a link.

## Deliberately not offered

- **Editing workflow files** — the App has no `workflows` permission.
- **Submitting reviews** (approve, request changes, comment) — the GitHub page shows reviews; the Git
  page's existing review route acts through the checkout's own sign-in.
- **Force pushes** and any ref update other than creating and deleting a branch.
- **Editing branch protection or rulesets** — shown read-only, changed on GitHub.
- Creating or editing labels (only applying existing ones), assignees, milestones, comments.
