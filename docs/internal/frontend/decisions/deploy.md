# Decisions: Deploy

Why these pages look as they do. The stories were told in `design-system.md` §14 and §15 until 0.9.0
and are moved here verbatim, under a heading per page. They are history: a rule they state is in
[`../standard.md`](../standard.md) or the page it links to, and where a story and the standard
disagree, the standard wins.

## Settings tabs took readings, then gave them up (§15 pass 2)

**A page you *configure* is not exempt.** §16 makes this argument about `/deploy/new` and it
generalises to every settings tab in the product: the deployment Runtime tab ran ten fields, four
selects and two switches with not one figure among them, so none of the three loud things this
system trades decoration for could fire, and the screen had nothing on it a reader could find
without reading. It opened on four readings — where it listens, what it may use, how it is
replaced, and what it can reach — until the operator asked for them to go (below), and each was a
fact the form beneath it sets, drawn from the draft rather than the saved revision, because what
you are setting is what the page is about. A reading was `warning` where the *absence* of an
answer is the answer: an uncapped container and a port open on every interface are the two facts
an operator wants off that page without opening a fold. Two of them read the running container
as well as the form: the memory limit was drawn against what the live release peaked at in the
last hour, and the release strategy is checked
against the rule the executor applies at the next deployment — a writable mount, a fixed host
port or the host network cannot run two releases side by side, and blue/green on such a plan was
offered there and then refused at start. Build took the pass it had skipped (what it builds with,
the last build read from the live release's own build step, the release tasks, the build
variables with a warning where a secret would be compiled into browser code), and Domains,
Storage, Databases and Automation opened on four readings each.

## The rest of the section's figures (§15 pass 2)

The rest of the deployment section took the pass as a question of *which* figures. The fleet
opened on four the chips beneath it cannot say — how many projects are live, requests a minute
across the fleet, the share of them failing, the build slots in use. The Logs page's readings
each carried their last hour, as the host Overview's do, until 27 Nov 2026, when the page
became a workbench and its figures one sentence about the window on screen (`window-headline.tsx`).

## Notifications and Credentials (§15 pass 2)

Notifications took it as well. Its four (channels, delivered and failed in the last day, the
last message) went to the Channels header, which counts the channels and the paused, and to a
list of the recent messages beside the cards: the day's delivered, failed and retrying are the
counts in that list's header — a message retried until it went out is one row and counts once,
as delivered — and the last message is its first row, naming the run it announced.

Credentials took it after that. Its four (held and across which hosts, in use, never used, last
used) each said what a card says where the credential is: the count and the hosts went to the
Saved credentials header, the In use rule counts what a source reads through and each card names
the projects that do, one never used says so on its own card in amber, and when each was last
used is beside its name. The GitHub App's state stays in its own section.

## The project pages (§15 pass 2)

Three deployment pages took it in the same pass. A project's General settings did because the
project identity line already is that page's reading line: each figure of the old Project
card went beside the control that sets it, and the page's doc comment names where. Variables took the
`/git` exit exactly — every count (all, pending, secret, config, reaching the build, the runtime
or a release task, holding a reference) is a filter chip over the list, where it also narrows
to what it counts. And a
project's Runtime page carries each count in the header of the block it counts. Its five moving
readings stood as tiles over the charts they move on until the operator asked for the tiles to
go; each now heads its own chart as the chart's reading now — the processor's share of a core
or its quota beside Processor, memory against its limit beside Memory, in and out beside
Network, read and written beside Disk, the process count against its limit beside Processes —
with a short meter where there is a ceiling, and what the tiles' hints said (the counters since
the container started, the quota and limits) is a fact list beside the Processes chart. In and
out stay two readings because a flood and a large download share a sum.

Deployment history also takes this exit: each pair of readings stands over the chart it explains.
Success and weekly frequency sit over releases per day; median duration and recovery time sit over
release time per day, which draws the same days at the same height beside it, with the window's
median as a dashed rule and the slowest day naming the top of its scale. Why the releases failed
runs under both, each cause a share of every failed release — and a lone cause a line with its
count, since a bar with nothing beside it compares nothing. The timing was a list of two figures
set at the far edge of their labels over a trend with no scale, and the reason a single full bar:
a drawing that cannot be read off is not a reading. The four readings keep their basis and window
without a separate strip of tiles over the records.
The project Overview retains its delivery tiles. History's counted status filters use the
underlined view-strip look as toggle buttons; filtering the same records does not make them ARIA
tabs. Switching status or environment reserves the results' height for the page visit, so a
shorter list does not clamp the shell's scroll position. These rows skip the arrival stagger so
a filter does not replay the entrance of the same history.

The rest of the deployment section took the exit in 0.7.1, because the operator asked for every
row of figures at the top of a deployment page to go. The fleet starts on its runs in flight:
each card carries its own traffic, the per-state counts are the chips', and a site failing badly
is an Attention finding. The Build, Runtime, Domains, Storage, Databases and Automation settings
start on their first section, whose head says what it currently is — Runtime's limits are drawn
against the last hour's peak beside their fields and a blue/green plan the executor would refuse
is said at the strategy, Domains' warning about a public bind address sits in its section, and
Databases' cards say whether the gating job dumps each one. Logs was the one deployment page that
kept its row until 27 Nov 2026, when the operator asked for no stat cards there and the row became
the window's own sentence ([request observability](../../deployments/request-observability.md#frontend)):
four readings of the ingress's last hour that no row or chip says. Its fifth,
Container, went — the Events tab already counts the hour's disruptions.
