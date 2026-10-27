# Deployment notifications and commit statuses

Every run announces its outcome to the audience outside the transcript. Two mechanisms share one
engine hook: notification channels (chat, e-mail, signed webhooks) and commit statuses (with preview
comments) on the Git host the source lives on — GitHub, GitLab, Gitea or Forgejo.

## Run observers

`deploy.RunObserver` receives `RunStarted` when a worker takes a run's first claim and
`RunFinished` once the run is terminal, through **every** path — `finishSuccess`, `failRun`,
`finishCancellation`, a reconciliation decision after a backend restart, or a cancellation before
any worker claimed the run. Observers run detached from the worker with a 30-second deadline, their
errors are logged and never returned, and `Engine.Shutdown` drains them so a run that finished a
moment before the process stopped still reaches its audience.

Before this hook existed, delivery happened inside the `notify` release step, which is the last
step of a successful release path; failed, rolled-back and cancelled runs never notified anyone.
The step remains for the scheduled-chain gate and records which channels select the outcome
(`channels`, `deliveredBy: "run observers"`); it no longer delivers.

## Channels

`deploy_notification_channels` gained additive `kind`, `target` and `config_enc` columns. Existing
rows are `webhook` channels whose URL is their display target.

| Kind | Credential (sealed in `config_enc`) | Validation | Delivery |
| --- | --- | --- | --- |
| `webhook` | Shared HMAC secret, optional sealed headers | `http(s)` URL | Signed JSON envelope, `X-JD-Signature-256` as before |
| `discord` | Webhook URL | `https://discord.com/api/webhooks/…` (or `discordapp.com`) | One embed with title, summary, fields, colour, run link |
| `slack` | Incoming webhook URL | `https://hooks.slack.com/services/…` | `text` plus mrkdwn blocks |
| `telegram` | Bot token + chat id | token `^\d{5,}:[A-Za-z0-9_-]{30,}$`, chat `-?\d+` or `@name` | `sendMessage` with HTML parse mode, previews disabled |
| `email` | SMTP host/port/security, username/password, from, up to 20 recipients | hostname or IP, port 1–65535, `starttls`/`tls`/`none`; a username requires TLS; RFC 5322 addresses | Plain-text mail, RFC 2047 subject, 15-second dial and command deadline |
| `teams` | Workflows webhook URL (its `sig` query parameter authorizes the post) | `https`, host under `.logic.azure.com` or `.powerplatform.com`, `/workflows/` in the path, a `sig` parameter; an Office 365 connector (`*.webhook.office.com`, retired by Microsoft in May 2026) is refused with that reason | `{"type":"message"}` with one Adaptive Card 1.4: title coloured `attention`/`good`, summary, a FactSet of the fields, an `Action.OpenUrl` to the run |
| `mattermost` | Incoming webhook URL | `…/hooks/<20–64 letters or digits>` (under a subpath too), `https`, or plain `http` on a loopback address; no user info | One Slack-style attachment: outcome colour bar, title linked to the run, summary and fields with Markdown backslash-escaped |
| `ntfy` | Server URL (default `https://ntfy.sh`), topic, optional access token | topic `^[-_A-Za-z0-9]{1,64}$`, token `^tk_[A-Za-z0-9]{29}$`; the server rule below | JSON publish to the server root: title, plain-text fields, priority 4 for bad news and 3 otherwise, `click` and one `view` action to the run, message kept under 4000 bytes; token as `Authorization: Bearer` |
| `gotify` | Server URL, application token | token `^[A-Za-z0-9._-]{10,64}$`; the server rule below, with a token always | `POST <server>/message` with `X-Gotify-Key`; priority 8 for bad news and 5 otherwise; the run link in the text and as the `client::notification` click |
| `pushover` | Application token, user or group key | both `^[A-Za-z0-9]{30}$`, as Pushover documents them | `POST https://api.pushover.net/1/messages.json` as JSON; title ≤ 250 and message ≤ 1024 characters, `url`/`url_title` to the run, priority 1 (sounds through quiet hours) for bad news and 0 otherwise |

Webhook URLs for Discord, Slack and Mattermost are credentials: the `url` column stores a masked form
(host and every path segment except the last) and the sealed configuration holds the real one. A Teams
URL's credential is its query, and its target is only `https://<host>/workflows/••••`. An ntfy
channel's target is `<server>/••••`: on a public server whoever knows the topic reads every message, so
the topic is held like a token. A Gotify channel shows its server, and a Pushover channel
`Pushover user` and the user key's first three characters. List and update responses never include a
credential. Editing a channel with a blank credential field keeps the stored value — for ntfy, Gotify
and Pushover every field is kept that way, the server URL included — and a channel's kind cannot change
after creation. A kept token cannot be cleared by blanking it; a channel that should stop sending one is
made again.

"Bad news" is a message rendered red: a failed run or an alert firing (`notificationMessage.urgent`).
The push services raise its priority so a phone makes noise for it and for nothing else.

The ntfy and Gotify server URL (`notificationServer`) is an `http` or `https` address with a host and
nothing else — no user info, query or fragment, since it is shown as the target — returned without a
trailing slash. Whenever a token goes with it (always for Gotify, when one is set for ntfy) it must be
`https`, unless its host is a loopback address (`localhost`, `127.0.0.0/8`, `::1`), parsed rather than
matched by prefix: the backend shares the host's network, so a server published on `127.0.0.1` is
reached without crossing a network. This is the rule SMTP sign-in is held to. Mattermost's webhook URL
takes the same loopback exception.

Two more events reach a channel without being selected on it: `traffic.firing` and `traffic.recovered`, sent by a traffic alert rule to the channels the rule names (see [`request-observability.md`](request-observability.md)); a webhook receives the envelope with an `alert` block, the providers a rendered sentence with the reading and the limit. Its `url` opens the project's Logs page on the requests around the moment the rule changed state (`?view=requests&moment=`), not on whatever the last hour holds when it is read.

Proxy rules likewise send `proxy.alert.firing` and `proxy.alert.recovered` to the channels the rule
names, or every enabled channel when it names none. A webhook receives the envelope's `proxy` block;
chat and e-mail render the kind, subject and reading. Kinds include failed certbot renewal, served
certificate drift and a watched endpoint below its chosen minimum TLS grade. The Proxy Alerts panel
owns these rules and their history; see [Proxy alerts](../backend/databases-proxy-platform.md).

Database alert rules send `database.alert.firing` and `database.alert.recovered` the same way: to the
channels the rule names, or every enabled one. A webhook receives the envelope's `database` block
(rule, kind, connection or backup job, label, the reading with its line, since when, `test`); chat and
e-mail render "Database disk filling up — shop" with the reading as the summary, and an e-mail's link
line reads "Open it in Just Dashboard" rather than "Open the run". A database's Alerts page owns these
rules and their history; see [Alerts](../backend/databases-proxy-platform.md#alerts-told-when-a-server-or-its-backups-go-wrong).
Like the proxy's and the traffic alerts' messages they are run-less deliveries (run 0), recorded in a
channel's history and never deduplicated or retried.

Host alert rules send `host.alert.firing` and `host.alert.recovered` the same way: to the channels the
rule names, or every enabled one. A webhook receives the envelope's `host` block (rule, kind, subject —
`cpu`, `memory`, a mountpoint, a container's name or `job:<id>` — label, `server`, the machine's name
since one channel often hears from several dashboards, the reading with its line, the threshold, since
when, `test`); chat and e-mail render "Disk filling up — /var" with the reading as the summary, a
field naming the subject (Server, Filesystem, Container or Backup job) and one naming the server unless
the subject already is it, and an e-mail's link line reads "Open it in Just Dashboard". The link opens
Metrics for CPU, memory and disks, the container's own page, or the backup job's. The host's Alerts page
under Metrics owns these rules and their history; see
[Host alerts](../backend/observability-security.md#host-alerts-told-when-the-machine-itself-goes-wrong).
They are run-less deliveries too.

Run events selectable on a channel are a closed vocabulary: `run.started`, `run.succeeded`, `run.failed` (includes
`failed_activation` and `rolled_back`), `run.cancelled` (includes `superseded`). An empty list
selects everything. The historical `run.finished` value remains valid as "every terminal outcome",
so channels created before failure notifications existed start receiving them without an edit.
Unknown events are rejected.

An automatic instant rollback (`deploy/rollback_watch.go`) that succeeds raises
`release.rolled_back` rather than `run.succeeded`: the run went well, the release it took traffic from
did not. It is not selectable on its own; it reaches every channel that selects `run.failed` (or
`run.finished`, or everything), and the providers render it red — "release #42 rolled back
automatically", the reading that decided it, and that the previous release serves again from its warm
container. An instant rollback that fails raises `run.failed` as any run does. Every instant rollback's
envelope, automatic or pressed by hand, carries a `rollback` block: `automatic`, `releaseNumber` (the
release taken back), `restoredReleaseNumber` (the warm one serving again), `reason` and, for an
automatic one, `rule` (`error_rate` or `latency`).

Deliveries are recorded per `(channel, run, event, attempt)` with only a status class
(`2xx`, `5xx`, `network`, `smtp`, `sealed`); response bodies are discarded. The dispatcher skips a
channel that already has a delivered row for the same run and event, so a reclaimed lease or a
restart cannot send the same message twice.

### Envelope

The signed webhook body keeps its six historical fields (`event`, `runId`, `projectId`,
`environmentId`, `state`, `sentAt`) and adds `projectName`, `environmentName`, `runNumber`,
`operation`, `trigger`, `actor`, `sourceRef`, `sourceRevision`, `endpoint`, `terminalCode`,
`terminalReason`, `durationSeconds` and `url`. Provider kinds render the same facts; a failure whose
terminal code is a named cause (a build, runtime, release-task or source cause rather than a code
that only says where, such as `build_failed`) adds a Cause field with its title before the reason,
which is the cause's sentence. A commit status, with room for a few words, carries the title. `url` points
at the run page under the dashboard's own endpoint, read from the self-configuration report and
cached for five minutes.

### API

- `GET /deploy/notifications` lists channels (id, name, kind, masked url/target, events, enabled),
  each with its history as the page draws it: `lastDelivery`, the newest attempt (status, event,
  response class, when, and `nextAttemptAt` while a retry is ahead — `failed` with one is retrying,
  without one given up), and `recent`, the last fourteen outcomes oldest first, each marked `test`
  when it was sent from the page. An e-mail channel also carries `via`, the mail server it hands
  messages to: the host is not a credential — the sign-in beside it is — so it alone is read back out
  of the sealed configuration, and a configuration the key cannot open leaves it unnamed rather than
  failing the list.
- `GET /deploy/notifications/options` returns the kinds, events and SMTP security modes.
- `POST /deploy/notifications` creates one; `secret` is returned only for the webhook kind.
- `PUT /deploy/notifications/{channel}` edits name, events, enabled and configuration.
- `PUT /deploy/notifications/{channel}/enabled` pauses or resumes without the configuration.
- `POST /deploy/notifications/{channel}/test` sends a rendered test message and answers 502 with
  the provider's status class on failure.
- `GET /deploy/notifications/{channel}/deliveries` lists recent attempts, the last 50 or `?limit=`
  of them (1 to 200; anything else reads 50), each naming the run it announced — `projectId`,
  `projectName` (an archived project's own name) and `runNumber`, so a row can say "api · run #12"
  and link to it; a test message and a run since purged have none. `DELETE` removes.

The history rides the list rather than a request per channel: `NotificationChannelsWithHistory` reads
every channel's newest fourteen attempts in one windowed statement, and one more for the e-mail
channels' servers. Create, update and enable responses do not carry it, so the page reads the list
again after a change. The dispatcher reads channels on every run through `ListNotificationChannels`,
which stays without it. Unlike a webhook trigger's summary (see
[implementation](implementation.md)), a channel's history is not an administrator's reading: the list
and the delivery log it summarises are both open to any signed-in account, and only the mutations
below need `system.admin`.

All mutations require `system.admin` and are audited as `deploy.notification.*`.

## Commit statuses

`deploy.CommitStatusPublisher` is a second observer. For a run whose environment source is a Git
remote on a host it can report to, it posts `pending` on start and `success`, `failure` or `error` on
the terminal state for the exact commit the run deployed, named `just-dashboard/<environment slug>` and
linked to the run — so a staging run reports as `just-dashboard/staging`, and promoting its release
reports the same commit again as `just-dashboard/production`. Restarts, stops, starts and preview
removals deploy no new commit and post nothing. A notification names its environment in the title and a
promotion's operation as "Promotion from staging".
A status that cannot be posted never changes a run's outcome; since the host's answer is otherwise
only a log line, the publisher keeps the newest outcome per environment (see
[What Settings shows](#what-settings-shows)).

### GitHub

A `github.com` remote (HTTPS, SSH or `git@` forms; GitHub Enterprise hosts are not GitHub here) is
posted to `repos/{owner}/{name}/statuses/{sha}` as the dashboard's [GitHub App](github-app.md) when one
is connected and installed on the repository, and through the dashboard's own `gh` credential otherwise
(or when the App is not installed there). A missing `gh`, an expired token or a repository the token
cannot write are recorded outcomes.

### GitLab, Gitea and Forgejo

`deploy.ForgeClient` speaks the REST APIs of GitLab and of Gitea, which Forgejo (Codeberg among them)
shares. Nothing about the host is configured beyond a token:

- **Which software.** `gitlab.com`, `codeberg.org` and `gitea.com` are known. Any other host is asked:
  `GET <prefix>/api/v1/version` answers anyone on Gitea and Forgejo (Forgejo's version reads
  `11.0.16+gitea-1.22.0`, which is how the page tells the two apart), and `GET <prefix>/api/v4/version`
  answers a GitLab token — or, without one, refuses with an `X-Gitlab-Meta` header that still identifies
  GitLab. Gitea repositories are always `owner/name`, so anything before those two path segments is
  the instance's subpath; a GitLab project nests groups, so each split of the path into "where GitLab
  is" and "the project" is tried in turn. An answer is kept for an hour, a host that answered as
  neither for two minutes. A redirect is never followed.
- **Which token.** The source's own credential when it is an HTTPS token (`git_bearer` or
  `provider_token`) for that host; otherwise the newest `provider_token` whose target names the host
  (with its port when it has one). An SSH-cloned or public source therefore reports once a provider
  token for its host is saved. `PlanningStore.ForgeToken` implements the rule, and a successful post
  stamps the credential's `lastUsedAt`.
- **Transport.** A token goes only to the remote's own host, over HTTPS — or plain HTTP to a loopback
  address, the rule every other credentialed call follows. An `ssh://` or `git@` remote is reported to
  the host's HTTPS side. The API is called with `Authorization: Bearer` on GitLab and
  `Authorization: token` on Gitea.
- **Calls.** GitLab: `POST /api/v4/projects/<url-encoded path>/statuses/<sha>` with `state` `running`,
  `success`, `failed` or `canceled` (GitHub's `pending`/`success`/`failure`/`error`), `name` and
  `target_url`. GitLab shows the statuses as an external pipeline on the commit and its merge requests; a
  second deployment of the same commit adds a status rather than being refused, and the pipeline
  settles on the newest. Gitea and Forgejo: `POST /api/v1/repos/<owner>/<name>/statuses/<sha>` with
  GitHub's states and `context`. Descriptions are cut to 140 characters.
- **Scopes.** GitLab: `api`, at least the Developer role. Gitea and Forgejo: `write:repository`, plus
  `write:issue` and `read:user` for preview comments. A refusal is recorded in the host's terms — a 403
  names the scope that host needs and appends the host's own message (GitLab's `error_description`,
  Gitea's `message`).

## Pull request comments

`deploy.PullRequestCommenter` is a third observer. For a run whose environment is a preview created by
a GitHub, GitLab or Gitea trigger it keeps one comment on the pull or merge request — found again by a
`<!-- just-dashboard:preview:<environment id> -->` marker at the start of its body and edited in place —
that reads Building, then Ready with the preview's address, Failed with the terminal reason, or Removed
once the preview is torn down, with the commit and a link to the run. Production runs produce no
comment.

On GitHub the comment speaks only as the GitHub App; a repository the App is not installed on gets
none. On GitLab (merge request notes, paged through `X-Next-Page`) and Gitea/Forgejo (issue comments)
it is written with the token the statuses use, and only a comment that token's own account wrote is
edited: the account is read once an hour from `/api/v4/user` or `/api/v1/user`, so a marker quoted by
somebody else, or in a GitLab system note, is never overwritten even with an administrator's token. A
comment is written only when the trigger's repository is the one the preview builds and the host
answers as the trigger's provider.

The per-environment policy row gained an additive `commit_statuses` column (default on), exposed as
`commitStatuses` on `GET …/git-watch` and `PUT …/git-policy` and as **Report each release as a
<host> commit status** in Settings → General's Automatic deployment section (**Report each release as a
commit status** on `/deploy/new`, before the host is known). It is excluded from the policy decision
key, so toggling it cannot invalidate cached polling decisions. It governs statuses; preview comments
follow the trigger.

## What Settings shows

`deploy_forge_reports` (additive; one row per environment and kind, `status` or `comment`, removed with
its environment) keeps the newest outcome: provider, repository, commit, state, run, whether the host
took it and, when it did not, why. A preview comment's row belongs to the environment whose trigger
opened the preview, which is the one Settings shows.

`GET …/git-watch` carries `commitStatus` from `CommitStatusPublisher.Describe`: `provider` (`github`,
`gitlab` or `gitea`), `product` (adds `forgejo` and `codeberg`, for the name and logo), `host`,
`repository`, `context`, the `credential` a GitLab or Gitea status is written with, `ready` and a
`reason` when it is not, and `lastStatus`/`lastComment`. The switch is named for the host, and the line
under it says one of: where the last status went and with which token; that a provider token for the
host is missing, linking to Credentials; or, in the warning tone, that the host refused the last status
or preview comment, with the token and the host's reason.

On Credentials a provider token lists `reportsForProjectIds` — the projects whose statuses it writes by
the rule above — and is filed under **In use** with "writes statuses for N projects". Nothing deploys
through it, so it is not `usedBy`, and removing it is not refused: the confirmation says the statuses
stop until another token for the host is saved.

## Verification

- `notifications_test.go`: validation matrix (every kind, including the cleartext-token and
  loopback rules, `127.example.test` not being loopback, and Teams' retired connector), every push
  setting kept on edit, legacy event alias, rendering for every outcome,
  a TLS grade alert's firing and recovery titles, a database alert's title, fields and e-mail link,
  a host alert's title, subject and server fields (the server named once) and e-mail link,
  dispatcher delivery of a failed run with deduplication, disabled channel and start-only
  selection, Discord/Slack/Telegram and ntfy/Gotify/Pushover/Mattermost/Teams payload shapes and
  credential headers through a rewriting transport, push priorities only for bad news, Pushover's and
  ntfy's length limits, e-mail through a captured mailer, credential masking in listings, and the
  notify step's new contract.
- `notifications_live_test.go` (`JD_DEPLOY_LIVE=1`, Docker): real ntfy (anonymous publishing
  denied, so the channel without a token is refused with `4xx`), Gotify and Mattermost servers on
  loopback each receive a failed run through `DeliverNotification`'s default client, read back from
  the server's own API — the title, the reason, the priority and the run link.
- `run_observer_test.go`: observers hear about failed, successful and queued-cancelled runs; a slow
  observer changes nothing about the run.
- `forge_reports_test.go`: every remote shape, Gitea under a subpath and GitLab with nested groups found
  by asking the host (and GitLab without a token), unsupported hosts remembered, no token over plain
  HTTP to another machine, each host's paths, states, names and auth header, one comment kept per
  preview and another author's marker left alone, a refusal in the host's terms, the token precedence,
  the publisher and the reading with no token and then with one, the policy switch, a GitLab merge
  request comment and a trigger for another repository refused, and which projects a provider token
  writes for.
- `forge_reports_live_test.go` (`JD_DEPLOY_LIVE=1`, Docker; `JD_FORGE_LIVE_GITLAB=1` adds GitLab, about
  five minutes to start): real Gitea 1.24, Forgejo 11 and GitLab 18.4 servers, each with a pull or merge
  request; the statuses and the single edited comment are read back from the server's own API, a token
  with too few scopes is refused with 403, and each host is named by asking it.
- `deploy-settings-a.spec.ts`: the switch named for GitLab and Forgejo, the missing-token line and its
  link, a refusal with the token's name; `deploy-credentials.spec.ts`: a status-writing provider token
  under In use, and its removal dialog.
- `automation_readings_test.go`: each channel carries its newest attempt and last fourteen outcomes
  oldest first, an e-mail channel its mail server, and each delivery the project and run it
  announced (none for a test message).
- `github_status_test.go`: pending/failure/success posting with deduplication, policy opt-out,
  restart and foreign-remote suppression, remote parsing, and the decision key staying stable.
- `github_comments_test.go`: a GitHub preview's comment through Building, Ready and Removed, the
  per-run deduplication, and a Gitea preview never reaching the GitHub poster.
- Browser (`deploy-projects.spec.ts`, `deploy-notifications.spec.ts`, `deploy-settings-a.spec.ts`):
  a Discord channel's creation, pause/resume, test delivery, delivery history and removal; the
  picker's ten kinds, an ntfy channel's generated topic and its refusal to send a token over plain
  http, a Teams URL named as a retired connector, and a Pushover edit keeping its keys until
  *Replace delivery settings*; each
  channel saying how its last message went on one line with its verbs on the card's middle, the
  recent messages and the last day's counts over them, and a channel's sheet reading every attempt; an e-mail edit that
  waits for its host, sender and recipients, and a Discord edit that keeps its webhook hidden until
  *Replace delivery settings* is opened; and the GitHub commit-status switch in Settings → General's
  Automatic deployment section.
- `notifications_reliability_test.go`: eight concurrent observers deliver exactly once; a failed
  delivery schedules a retry, a repeated observer call cannot jump the backoff, the sweeper re-sends
  when due and stops after the third attempt; Slack escaping and Discord length caps.
- Not verified here: a real Discord, Slack, Teams, Telegram, Pushover or SMTP endpoint, and a real
  GitHub status post. They depend on external accounts; the test endpoint exercises the same code
  path. The Teams card follows the Workflows template's documented shape, which an operator's own
  flow can change.

## Delivery reservation and retries

`DeliverNotification` reserves the attempt before it sends. Inside one write transaction (the store
opens write transactions `IMMEDIATE`, so two observers serialize here) it refuses when a row for
(channel, run, event) is `delivered`, `pending` and younger than five minutes, or `failed` with a
retry still ahead; otherwise it marks stale pending rows `failed`/`interrupted`, clears any older
retry, inserts attempt `MAX+1` as `pending` and commits. The send happens outside the transaction and
the row is updated with `delivered` or `failed`, the response class and, for a run delivery that
failed before its third attempt, `next_attempt_at` (one minute after the first failure, five minutes
after the second). Test deliveries (run 0) are never deduplicated or retried.

`NotificationDispatcher.RetryFailedDeliveries` rides the automation scheduler's 30-second tick
(`AutomationScheduler.WithSweep`): it selects failed rows whose retry time has passed, whose channel
is enabled and that have no later attempt, rebuilds the envelope from the persisted run and delivers
through the same reservation. A run that can no longer be read has its retry cleared. Slack text is
mrkdwn-escaped (`&`, `<`, `>`) and Discord titles, descriptions and field values are truncated to the
limits Discord enforces, so a project name cannot break a link or lose a message.
