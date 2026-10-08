# Docker overview overhaul

The screenshots in this directory record the `/docker` overview before and after its redesign, at
1440 and 1280 wide and on a 390-wide phone, each as the first screen (`*-fold-*`) and the whole page
(`*-page-*`).

## Before

[The page](before-page-1440.png) opened on four tiles — running, runtime health, attention and
compose stacks — over a list of the containers that were not running, the attention list and the
compose projects. Nothing on it moved, the containers that were running were not on it at all, and
nothing said which container was using the machine or what had just happened.

## After

[The page](after-page-1440.png) opens on Docker's identity line: its version, the host, the storage
driver and cgroup version, the running containers, the compose projects up and the images, with two
verdicts at its end — the containers failing, which narrows the table to them, and the attention
issues, which goes to the list. Under it:

- **Processor and Memory** — the five containers using the most of each as spans of one bar the size
  of the machine, fed by the containers socket, so each span eases and each figure glides with every
  frame; everything else in use on the host is one muted span.
- **Recent** — the last thing that happened to each container in the last day, live off the
  daemon's events. An OOM kill and its exit are one line, a stop's kill, die and stop are one, and a
  container that came back after crashing says how many times it did in the hour.
- **Containers** — every container as a framed table: the product it runs, its compose project in
  the project's own hue and its image, posture issues counted beside the name, its state with uptime
  and health or how it stopped (*killed for memory* rather than a bare 137), its last hour of CPU as
  a sparkline beside the live figure, memory against its limit or the heaviest, its published ports
  and its verbs. Failing containers come first. State chips count and narrow; project chips narrow.
  [Pressing the failing verdict](after-failing-1440.png) narrows the table to those containers.
  [On a phone](after-page-phone.png) each row is drawn down rather than across, with nothing dropped.
- **Attention**, unchanged, then the **compose projects** as lit cards, each with its services as a
  strip of their states, beside **Disk**.

Every figure the four tiles held is still on the page; `app/(dashboard)/docker/page.tsx` names where
each went, and `design-system.md` §15 records the exit.

## Fixture

The screenshots use the mocked host in `frontend/tests/browser/docker-fixture.ts`: three compose
projects and two standalone containers, one killed for memory twelve minutes ago, one failing its
health check, and one stopped cleanly two days ago. It is the same host
`tests/browser/docker-overview.spec.ts` checks.
