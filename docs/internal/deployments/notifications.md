# Deployment notifications and commit statuses

Every run announces its outcome to the audience outside the transcript. Two mechanisms share one
engine hook: notification channels (chat, e-mail, signed webhooks) and GitHub commit statuses.

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

Webhook URLs for Discord and Slack are credentials: the `url` column stores a masked form (host and
every path segment except the last) and the sealed configuration holds the real one. List and
update responses never include a credential. Editing a channel with a blank credential field keeps
the stored value; a channel's kind cannot change after creation.

Events are a closed vocabulary: `run.started`, `run.succeeded`, `run.failed` (includes
`failed_activation` and `rolled_back`), `run.cancelled` (includes `superseded`). An empty list
selects everything. The historical `run.finished` value remains valid as "every terminal outcome",
so channels created before failure notifications existed start receiving them without an edit.
Unknown events are rejected.

Deliveries are recorded per `(channel, run, event, attempt)` with only a status class
(`2xx`, `5xx`, `network`, `smtp`, `sealed`); response bodies are discarded. The dispatcher skips a
channel that already has a delivered row for the same run and event, so a reclaimed lease or a
restart cannot send the same message twice.

### Envelope

The signed webhook body keeps its six historical fields (`event`, `runId`, `projectId`,
`environmentId`, `state`, `sentAt`) and adds `projectName`, `environmentName`, `runNumber`,
`operation`, `trigger`, `actor`, `sourceRef`, `sourceRevision`, `endpoint`, `terminalCode`,
`terminalReason`, `durationSeconds` and `url`. Provider kinds render the same facts. `url` points
at the run page under the dashboard's own endpoint, read from the self-configuration report and
cached for five minutes.

### API

- `GET /deploy/notifications` lists channels (id, name, kind, masked url/target, events, enabled).
- `GET /deploy/notifications/options` returns the kinds, events and SMTP security modes.
- `POST /deploy/notifications` creates one; `secret` is returned only for the webhook kind.
- `PUT /deploy/notifications/{channel}` edits name, events, enabled and configuration.
- `PUT /deploy/notifications/{channel}/enabled` pauses or resumes without the configuration.
- `POST /deploy/notifications/{channel}/test` sends a rendered test message and answers 502 with
  the provider's status class on failure.
- `GET /deploy/notifications/{channel}/deliveries` lists recent attempts; `DELETE` removes.

All mutations require `system.admin` and are audited as `deploy.notification.*`.

## GitHub commit statuses

`deploy.CommitStatusPublisher` is a second observer. For a run whose environment source is a
`github.com` remote (HTTPS, SSH or `git@` forms; GitHub Enterprise hosts and other providers are
ignored) it posts `pending` on start and `success`, `failure` or `error` on the terminal state to
`repos/{owner}/{name}/statuses/{sha}` with context `just-dashboard/<environment slug>` and a link to
the run. The status is posted as the dashboard's [GitHub App](github-app.md) when one is connected
and installed on the repository, and through the dashboard's own `gh` credential otherwise (or when
the App is not installed there). Restarts and preview removals deploy no new commit and post
nothing. A missing `gh`, an expired token or a repository the token cannot write are logged
warnings; a run's outcome is never changed by a status that could not be posted.

## Pull request comments

`deploy.PullRequestCommenter` is a third observer, and it speaks only as the GitHub App. For a run
whose environment is a preview created by a GitHub trigger it keeps one comment on the pull request —
found again by a `<!-- just-dashboard:preview:<environment id> -->` marker and edited in place — that
reads Building, then Ready with the preview's address, Failed with the terminal reason, or Removed
once the preview is torn down, with the commit and a link to the run. Production runs, previews from
other providers and a repository the App is not installed on produce no comment; a comment that
cannot be posted is a logged warning. `github_comments_test.go` covers the state sequence, the
per-run deduplication and the refusals.

The per-environment policy row gained an additive `commit_statuses` column (default on), exposed as
`commitStatuses` on `GET …/git-watch` and `PUT …/git-policy` and as **Report deployment status to
GitHub commits** in the Deployment policy editor. It is excluded from the policy decision key, so
toggling it cannot invalidate cached polling decisions.

## Verification

- `notifications_test.go`: validation matrix, legacy event alias, rendering for every outcome,
  dispatcher delivery of a failed run with deduplication, disabled channel and start-only
  selection, Discord/Slack/Telegram payload shapes through a rewriting transport, e-mail through a
  captured mailer, credential masking in listings, and the notify step's new contract.
- `run_observer_test.go`: observers hear about failed, successful and queued-cancelled runs; a slow
  observer changes nothing about the run.
- `github_status_test.go`: pending/failure/success posting with deduplication, policy opt-out,
  restart and foreign-remote suppression, remote parsing, and the decision key staying stable.
- Browser: channel creation for signed webhooks and Discord, pause/resume, test delivery, delivery
  history, removal, and the commit-status toggle in the policy editor.
- `notifications_reliability_test.go`: eight concurrent observers deliver exactly once; a failed
  delivery schedules a retry, a repeated observer call cannot jump the backoff, the sweeper re-sends
  when due and stops after the third attempt; Slack escaping and Discord length caps.
- Not verified here: a real Discord, Slack, Telegram or SMTP endpoint, and a real GitHub status
  post. Both depend on external credentials; the test endpoint exercises the same code path.

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
