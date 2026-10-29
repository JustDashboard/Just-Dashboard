# Right-sizing and the capacity forecast

What limits each deployment should have, when each disk fills, and whose volumes grow fastest — read
from the history the metrics recorder already keeps (`JD_METRICS_RETENTION`, a week by default). No new
collector runs for right-sizing; the forecast adds one slow sampler of the volumes deployments own.

## Right-sizing

**Source.** `metrics/sizing.go` holds the maths and the reader; `deploy/sizing_targets.go` reads what
each environment is (`PlanningStore.SizingTargets`); `api/handlers_deploy_sizing.go` joins them.

- **Which samples.** An environment's samples are `metric_container_samples` rows whose `container_id` is
  one of its container releases' runtime ids (`deploy_release_runtimes`, newest 200) — the identity the
  release recorded, never a name another container could carry. Rows from before `container_id` existed
  are unattributed and left out. The window is the whole retained record up to now.
- **One pass.** `Recorder.SizingUsage` reads those rows ordered by `(container_id, ts)` and folds them in
  Go: memory into whole-MiB buckets, CPU into half-percent buckets, the peaks, the distinct sample instants
  (two containers side by side in a blue/green cutover are one moment) and each container's CFS throttling.
  A week of one container is about 40,000 rows and 0.15 s; the window functions SQLite would run for the
  same answer took three times that. `RecentSizingUsage` keeps a read for five minutes per set of ids; the
  judgement on top is redone every request, so a limit saved a moment ago is judged at once.
- **CPU samples that measured nothing** — the first after a start, a counter reset, a gap of more than
  three intervals — stored 0 and are left out of CPU, as `ContainerRange` leaves them out of a chart.
- **Throttling** is new in the record: `cpu_periods` and `cpu_throttled`, Docker's cumulative CFS
  counters, additive nullable columns written only for a container with a CPU quota (no quota, no
  periods: NULL, never "no throttling"). The share is the differenced throttled periods over the
  differenced periods within each container, a restart's reset left out.
- **OOM kills** are the `oom` events of the live release's containers (by its `release-id` label, or the
  container id) in the Docker event log since the window began. The log is the in-memory ring the Events
  feed reads, so it knows only what happened while it listened; the answer carries `oom.watching` and
  `oom.since`. A persisted event record would replace this read without changing the rule.

**The maths** (`SizeMemory`, `SizeCPU`, `Size`). Percentiles are nearest-rank over the histograms,
answered at each bucket's upper edge, so never under the true value.

- *Memory*: the larger of the 95th percentile plus 25% and the peak plus 10% — memory is not
  compressible, and a limit under the peak is a kill — rounded up to 64 MiB below 1 GiB, 256 MiB to 8 GiB
  and 1 GiB beyond, at least 64 MiB. After an OOM kill under the live limit, at least 1.5× that limit:
  the fifteen-second sampler does not see the spike that killed it.
- *CPU*: the 95th percentile plus 50%, at least a quarter core (a start-up throttled into its readiness
  timeout fails the release), rounded up to 0.05 below one core and 0.25 above, capped at the host's
  cores. With throttling in 5% or more of the live release's periods, at least 1.5× the live limit.
- *Verdicts*, against the **saved** limit (the one that applies with the next release): **too tight**
  when the suggestion is above it (the reasons say which reading drove it); **oversized** at twice the
  suggestion and 256 MiB or half a core more; **no limit** when zero; otherwise **sized right**. The pressure
  rules use the **live** limit the pressure happened under, so once the suggestion is saved the same OOM
  kill does not raise it again. With less than a day of samples (`SizingHistory`: samples × interval)
  there is no suggestion, only the window and how much more it needs.
- *An environment's verdict* is the one that asks the most (too tight, oversized, no limit, right) and
  `offBy` is how far: suggestion ÷ limit when tight, limit ÷ suggestion when oversized. `apply` carries
  only the limits that are off, so a limit judged right is left as the operator set it.
- A **Compose** stack answers `status: "compose"`: its services keep the limits their file declares
  (implementation.md, runtime limits), so the plan has nothing to size. History off answers
  `status: "unavailable"`.

**Routes.** Both are reads; neither is audited or rate-limited beyond the API's own.

| Route | Who | Answer |
| --- | --- | --- |
| `GET /deploy/{id}/environments/{env}/sizing` | anyone who reads the deployment; a deploy key (`keyOpen`) | one environment, any kind |
| `GET /deploy/sizing` | the same; a narrowed deploy key gets its own deployments (`keyOpen`, narrowed by the handler) | every production and staging environment, Compose left out, sorted by verdict then `offBy` |

**Applying** is not a route of its own. The page reads the environment's configuration, writes the
`apply` values into `runtime.memoryMb`/`runtime.cpus` and saves the whole of it back with the revision
it read — `PUT /deploy/{id}/environments/{env}/configuration`, the Runtime settings' own save: the same
`system.admin`-in-a-session (or deploy key) gate, the same `deploy.configuration.update` audit, the same
plan validation and deploy-key host-authority guard, the same revision conflict, and the same "applies
with the next release" (the sizing then answers `pending: true`).

**UI.** The project overview's **Right-size** card (`components/deploy/right-size.tsx`, words in
`sizing-model.ts`) sits under Production: the verdict and *Apply suggested limits* in its head, the two
suggestions as figures with p95, peak and limit under each, the first reason of each limit that is off,
and the window it rests on; each figure's ⓘ states its rule. It draws nothing for a Compose stack or a
failed read. The fleet's **Right-size** panel under the projects lists only the environments that are
off, furthest first, with what an apply would change and an *Apply* per row; what is right or still
gathering history is a count in its head, and a fleet with nothing off draws nothing.

## Capacity forecast

**Source.** `metrics/forecast.go` (the fit and the readers), `api/handlers_capacity.go` (the route and
the volume sampler); `GET /system/metrics/capacity`, beside the other metrics reads.

- **The line** is ordinary least squares through hourly means of the retained samples (`LinearFit`),
  in bytes against days. Its **confidence** grades R² and span: high at R² ≥ 0.8 over three days,
  medium at 0.5 over one, low otherwise. Under six hours or three points there is no line
  (`insufficient_history`). A slope under 1 MiB a day is **steady**.
- **Filesystems** are `metric_mount_samples`, the ones in the newest sample only (one missing from it
  has been unmounted, as for `disk_filling`). Days to full start from the newest sample's used bytes,
  not the line's value: `(total − used) ÷ slope`. Sorted soonest first.
- **Volumes** are sampled into `metric_volume_samples (ts, name, size_bytes)` every quarter hour
  (`metrics.VolumeInterval`, its own loop so a cold disk walk never delays a host sample), pruned with
  the rest of the record. `sampleDeploymentVolumes` names the volumes deployments own — a saved plan's
  managed named-volume mounts and managed `docker_volume` storage dependencies, and the volumes Compose
  labelled with a live stack's project — and reads their sizes from Docker's cached disk walk, the one
  the Docker pages share; a size the walk did not reach is skipped, never kept as zero. The route reads
  Docker's sizes at the moment too (five seconds at most), so a volume is sized before its first sample;
  a volume a listing Docker answered does not have is left out.
- **Fastest-growing apps** sum each environment's volumes' slopes, fastest first.

**UI.** The Metrics page's **Capacity forecast** section, under Storage
(`components/metrics/capacity-forecast.tsx`, words in `lib/capacity.ts`): each filesystem's date in the
tone it deserves (under a week red, under a month amber) over its bar, with growth, confidence and what
the line was fitted to; and the five fastest-growing apps, each a link to its project. The section's ⓘ
states the method. With history off it draws nothing; the Storage charts already say so.

## Tests

- `go test ./internal/metrics -run 'Percentile|Histogram|Suggestions|Memory|CPU|Throttl|Overall|Size|SizingUsage|RecentSizing|LinearFit|Confidence|MountForecast|ShortRecord|Volume'`:
  the percentiles, rounding, headroom and verdicts, the OOM rule that does not escalate, insufficient
  history, the reader's moments, buckets and throttling, the cache, the fit and its confidence, the mount
  and volume readers and the volume record's expiry.
- `go test ./internal/deploy -run TestSizingTargets`, `go test ./internal/store -run TestCapacityMigration`.
- `go test ./internal/api -run 'TestSizing|TestApplyingASuggestion|TestOOMKills|TestOwnedVolumes|TestAppGrowths|TestCapacityForecast'`:
  the reads for a session, an ordinary key, a deploy key and a narrowed one; the apply through the
  configuration PUT with its audit, plan change, validation, revision conflict and deploy-key audit.
- Live, as root on a Docker host:
  `JD_DEPLOY_LIVE=1 go test ./internal/api -run 'TestLive(DeploymentVolumeSizesAreSampled|ThrottlingIsRecordedForACPULimitedContainer)' -count=1 -v`.
  The first writes 8 MiB into a real managed volume and reads it back through the sampler, the running
  recorder and the route; the second holds a busy loop to a tenth of a core and reads its throttled share
  (most of its periods) and its CPU peak back from what the running recorder kept. Both use the cached
  `alpine:3.20` (never pulled) and remove their volume and container.
- `bun test src/components/deploy/sizing-model.test.js src/lib/capacity.test.js` and
  `tests/browser/right-sizing.spec.ts` (mocked reads: the card and its apply, the fleet panel, the
  Metrics section at 1280 and 390).
