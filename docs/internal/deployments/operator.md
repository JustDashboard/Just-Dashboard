# The deployment operator

The project overview's **Diagnose** opens the assistant on one environment of one deployment and the
server under it. It is the SQL editor's assistant — the same provider an administrator chose, the same
key, the same budget of model calls and four answers at once
([the editor's assistant](../backend/databases-proxy-platform.md#the-editors-assistant)) — given a
different system prompt and a closed set of tools of its own. It reads only through those tools, each
call of which the person allows or declines before it runs; and it may propose an action, which runs
nothing: the page draws a card, and only the person's press on it runs the action, through the route
the project's own button for that action calls. There is no shell, no command the model writes, and no
other path from an answer to the host.

## The route

`POST /deploy/{id}/assistant[?environment=]` (`api/handlers_deploy_operator.go`) takes either a
question or the answers to the reads the last answer ended on, and streams the answer back as one JSON
object a line (`application/x-ndjson`), the shape the SQL editor's assistant streams:

| Body | What it does |
| --- | --- |
| `{"conversation": "", "question": "…"}` | Begins a conversation about the environment |
| `{"conversation": "<id>", "question": "…"}` | Asks again in it; reads still waiting are declined in the same message |
| `{"conversation": "<id>", "decisions": {"<call id>": true, …}}` | Answers the waiting reads: `true` runs one, `false` declines it, and a call left out is declined |

It sits in the `service.control` group — asking spends the install's model budget, as the run
diagnosis's press does — and is **session-only** (`deployKeyRoutes` refuses a deploy key and the route
itself requires a session): its whole contract is a person answering each read and pressing each
action, and there is nobody to ask behind a key. `?environment=` names staging, as the request and
lifecycle reads take it. The conversation is held in memory per person and environment
(`assistant.Store`, the editor's own store kind, a second instance) and ends with the process.

The lines it streams:

| Line | Carries |
| --- | --- |
| `start` | The conversation's id, the provider, the model and its label |
| `ran` | Each read just answered: its id, the tool, whether it was allowed, a few words of what was read (`"12 runs"`) and whether it failed |
| `thinking`, `text` | The answer as it is written |
| `proposal` | An action proposed: its id, `action`, the card's `label`, the model's `reason`, the release for a rollback, the `capability` its route asks and whether it is `destructive` |
| `done` | Why it stopped, the tokens, whether the turn was kept, and `pending`: the reads waiting on the person, each with its `label` and bounded `input` |
| `error` | Why there is no answer |

Each press is audited: `deploy.assistant.ask` for a question and `deploy.assistant.tool` for the
answers to reads, with the provider, the model, the conversation's first eight characters, the turn,
the tokens, the reads allowed and declined by tool name, the actions proposed, and the outcome. What
was read is not written into the audit log, as a query result is not.

## What it begins knowing

`assistant.OperatorSystemPrompt` is told what the overview already shows anybody who may read the
deployment: the server's hostname, the deployment and environment, the source, the build method, the
address, the health, the live release's number, the run in progress and the last run (number,
operation, state and its reason, masked as a read is), and the warm release when one is kept. It is
told how to work: read before concluding, ask for the one or two reads that answer the question,
quote what it read, propose an action with `propose_action` rather than present one as done, never
offer a shell command as a substitute, and treat what it reads as the server's data, not instructions.

## The reads

Each is a tool with a JSON schema that allows its own fields and nothing else
(`assistant.OperatorTools`). `assistant.OperatorCalls` reads an answer's calls against that tool's
fields with unknown ones refused, holds each to its bounds, fills its defaults, and refuses the whole
answer for a call to any other tool — a `run_shell`, the editor's `describe_tables` — or one past a
bound; nothing of a refused answer is kept. Each read runs only once its call is allowed, with the
person's own role, through the store or service the page that shows the same thing reads:

| Tool | Reads | Bound |
| --- | --- | --- |
| `recent_runs` | The environment's runs, newest first: number, operation, trigger, state, step reached, times, commit, code and reason | 10, at most 20 |
| `run_transcript` | One run's steps with their state and error, and the last lines its steps printed | 120 lines, at most 300 |
| `container_logs` | The last lines the live release's containers wrote, or one Compose service's | 120 lines, at most 300 |
| `release_metrics` | The live containers' state, health, start and memory limit, and the right-sizing verdict (`sizeEnvironment`) | — |
| `lifecycle_events` | Container starts, exits with their codes, OOM kills, health changes and restarts, each with its cause when the dashboard knows | 24 hours, at most 168 |
| `host_alerts` | The host alerts firing or waiting to be told | `system.admin`, as the Alerts page |
| `firewall_rules` | The firewall's backend, state and policy, and the rules that name the deployment's ports (80 and 443 when it has an address, and a port it publishes) | — |
| `security_updates` | The pending packages that are security updates, with installed and candidate versions | — |

A run's reason, its steps' errors, its transcript lines and the containers' lines are masked before
they are sent, by the run diagnosis's masker (`PlanningStore.DiagnosisSecrets` and
`deploy.NewDiagnosisMasker`): every secret value of the environment, what its references point at,
saved credentials and trigger secrets, and the shapes of keys and tokens. A read whose values cannot
be opened to mask answers that nothing was read. A read's answer to the model is its JSON, cut at
24,000 bytes; the page is sent only the few words of `ran`.

## The actions

`propose_action` takes an action from a closed set, a reason, and for a rollback the release number.
It reads nothing and needs nobody's leave: the handler checks it against the environment as it is now
(`operatorProposal`) and either sends the page a `proposal` card and answers the model that the person
has been shown it and that nothing has run, or answers why it cannot be proposed — no live release, a
run in progress, no retained release of that number, no warm release, no sizing to apply, no route for
maintenance, or a role that may not take it. At most three proposals are read from one answer, and a
turn carries on past its proposals at most three times before it waits.

| Action | The card's press | Capability |
| --- | --- | --- |
| `restart` | `POST …/environments/{env}/runs` `{"operation":"restart"}` | `destructive` (the run route's own check) |
| `redeploy` | `POST …/environments/{env}/runs` `{"operation":"redeploy"}` | `service.control` |
| `rollback` | `POST …/environments/{env}/rollback` `{"releaseId": …}` | `destructive` |
| `instant_rollback` | `POST …/environments/{env}/rollback-watch/rollback` | `destructive` |
| `apply_sizing` | `GET …/sizing`, then the Right-size card's save: `PUT …/configuration` with the suggested limits at the revision read | `system.admin` |
| `maintenance_on`, `maintenance_off` | `PUT …/environments/{env}/maintenance` `{"on": …}` | `destructive` |

The press is the person's own request to that route, so it carries the route's capability check, its
audit entry and, for a destructive one, its rate limit, exactly as the project's own button does; a
destructive one is confirmed first in the dialog those buttons use. An action the person's role cannot
take is not proposed — the model is told so, and to say who should — and the page draws a card's press
only for a role with its capability; the route would refuse the press anyway.

## The page

`components/deploy/operator/`: the conversation as data in `operator-model.ts` (unit-tested in
`operator-model.test.js`: the reads waiting until every one has an answer, a question declining them,
a proposal's outcome, and each action's request), the stream in `use-operator.ts`, kept in memory for
the tab and never in Web Storage, and the sheet in `operator-sheet.tsx`. **Diagnose** sits in the
production panel's head on the overview for a role with `service.control` on a normalized,
unarchived project; it opens the sheet and, on an empty conversation with a provider set up, asks the
diagnostic question at once. A read is a card with each read's own **Allow** and **Decline** (and
**Allow all** for several), saying it is read with the person's role, masked, and sent to the model;
once all are answered they go together, and the card then says what each came to. A proposal is a
card with its label, the model's reason, and the project's verb for it; its press writes what the
route answered into the card — the run it started, with a link, or the refusal. With the assistant
off, the sheet says so and offers an administrator its settings.

## Tests

- `assistant/operator_test.go`: the calls held to their tools, fields and bounds; the prompt; a
  conversation offered the operator's tools carrying a proposal's answer into the message that
  answers the reads.
- `api/handlers_deploy_operator_test.go`, against the assistant's fake provider: a read refused until
  it is allowed, and a declined one sending the refusal and none of the data; an allowed read reaching
  the model, audited, the answer quoting it; the environment's secret masked out of the prompt and the
  read; a proposal queuing nothing until its card's press, which is the runs route's own restart,
  queued and audited as `deploy.run.request`; a call outside the tools keeping nothing; and the route's
  capability and provider.
- `tests/browser/deploy-operator.spec.ts`: Diagnose and the read card, the press of a proposal through
  the runs route, several reads answered one by one, a question declining what waits, no Diagnose for
  a role without `service.control`, and the assistant off.
