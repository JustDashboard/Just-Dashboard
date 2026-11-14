"use client"

import { useNow } from "@/components/deploy/vocabulary"
import { InfoTip } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { LogoGlyph } from "@/components/logo"
import { Button } from "@/components/ui/button"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { duration } from "@/lib/format"
import { driftReading, systemdInstant, type DriftReport } from "@/lib/network-drift"
import { cn } from "@/lib/utils"
import { ShortDigest } from "@/components/network/drift/marks"

type Command = DriftReport["boot"]["execution"]["commands"][number]

/** What runs a boot command, by its binary: netfilter's `nft`, the dashboard's own helper, else the kernel's tools. */
function commandProduct(path: string) {
  const name = path.split("/").pop() ?? path
  if (name === "nft" || name.startsWith("iptables") || name.startsWith("ip6tables"))
    return "netfilter"
  if (name === "network-recovery" || name === "just-dashboard") return undefined
  return "linux"
}

function commandTone(command: Command) {
  if (command.exitStatus === undefined) return "unknown"
  if (command.exitStatus === 0) return "ok"
  return command.ignoreErrors ? "ignored" : "failed"
}

const COMMAND_RULE: Record<string, string> = {
  ok: "bg-success",
  ignored: "bg-warning",
  failed: "bg-destructive",
  unknown: "bg-muted-foreground/45",
}

/**
 * The boot unit as one line: systemd's mark, the unit over its four facts,
 * what systemd measured of its last activation in this boot, and its state.
 * It was a stack: the unit, a sentence in amber, a heading, another sentence,
 * a framed notice about reboot attribution and a fold of evidence. The reason
 * the unit differs is the line under its state now, and the caveat is the ⓘ
 * beside it.
 *
 * The activation's detail is a glance, so it opens from the line's end rather
 * than hanging under it: the run of the unit's `ExecStart` commands in the
 * order it runs them, each as the tool it is (`ip`, `bridge`, `tc` and
 * `sysctl` as Linux, `nft` as netfilter) over a rule in the colour of its
 * exit (green for 0, amber for a failure the unit ignores so the other
 * domains still restore, red for one it does not, grey where systemd kept no
 * outcome), then the evidence the reading rests on.
 *
 * The finish is said as how long ago, ticking with the page, wherever the
 * timestamp's zone can be read without guessing; the raw strings stay in the
 * evidence either way.
 */
export function DriftBoot({ boot }: { boot: DriftReport["boot"] }) {
  const now = useNow(1000)
  const activation = boot.execution
  const finished = systemdInstant(activation.finishedAt)
  const took =
    activation.startedMonotonicUs !== undefined && activation.finishedMonotonicUs !== undefined
      ? (activation.finishedMonotonicUs - activation.startedMonotonicUs) / 1e6
      : undefined
  const facts = [
    ["loaded", boot.loadState],
    ["enablement", boot.unitFileState],
    ["active", boot.activeState],
    ["needs reload", boot.needDaemonReload],
  ] as const
  const when = [
    finished !== undefined ? `${duration(Math.max(0, (now - finished) / 1000))} ago` : undefined,
    took !== undefined
      ? `ran ${took < 1 ? `${Math.round(took * 1000)} ms` : `${took.toFixed(1)} s`}`
      : undefined,
  ].filter(Boolean)
  return (
    <Panel plain aria-label="Boot unit">
      <PanelHeader title="Boot unit" />
      <PanelBody>
        <div
          data-slot="boot-unit"
          className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-2 rounded-xl border bg-choice-surface px-3 py-2.5 sm:min-h-14 sm:flex-nowrap"
        >
          <div className="flex min-w-0 flex-1 basis-64 items-center gap-3">
            <ProductLogo id="systemd" size="sm" />
            <div className="min-w-0">
              <p className="truncate font-mono text-body font-medium" title={boot.unit}>
                {boot.unit}
              </p>
              <p className="flex flex-wrap gap-x-3 text-hint text-muted-foreground sm:flex-nowrap sm:truncate">
                {facts.map(([label, value]) => (
                  <span key={label} className="shrink-0">
                    {label}{" "}
                    <span
                      className={cn(
                        "text-foreground",
                        !value && "text-muted-foreground",
                        (value === "disabled" || value === "masked" || value === "yes") &&
                          "text-warning",
                      )}
                    >
                      {value || "unknown"}
                    </span>
                  </span>
                ))}
              </p>
            </div>
          </div>

          <div aria-label="Last activation" role="group" className="min-w-0 sm:w-52 sm:shrink-0">
            <p className="truncate text-xs font-medium">
              {activation.status === "unrecorded"
                ? "No recorded activation"
                : `Last activation ${activation.status}`}
            </p>
            <p
              className="numeric truncate text-hint text-muted-foreground"
              title={when.length === 0 ? activation.reason : undefined}
            >
              {finished !== undefined ? (
                <time dateTime={new Date(finished).toISOString()}>{when.join(" · ")}</time>
              ) : when.length > 0 ? (
                when.join(" · ")
              ) : (
                (activation.reason ?? "no outcome kept")
              )}
            </p>
          </div>

          <div className="min-w-0 sm:w-44 sm:shrink-0">
            <p className="flex items-center gap-1.5">
              <Status {...driftReading(boot.status)} />
              <InfoTip label="What an activation does not establish">
                Reboot attribution is unknown: a measured unit activation does not establish that it
                ran at boot or that reboot restoration succeeded.
              </InfoTip>
            </p>
            <p className="truncate text-hint text-warning" title={boot.reason}>
              {boot.reason ?? "\u00a0"}
            </p>
          </div>

          <Popover>
            <PopoverTrigger asChild>
              <Button size="xs" variant="outline" className="shrink-0">
                Activation
              </Button>
            </PopoverTrigger>
            <PopoverContent align="end" className="w-96 space-y-4">
              <section aria-label="Commands the unit ran" className="space-y-2">
                <p className="text-xs font-medium">Commands the unit ran</p>
                {activation.commands.length > 0 ? (
                  <ol className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(3.75rem,1fr))] gap-1.5">
                    {activation.commands.map((command, index) => (
                      <CommandStep key={index} command={command} />
                    ))}
                  </ol>
                ) : (
                  <p className="text-hint text-muted-foreground">
                    {activation.reason ?? "No per-command execution outcomes are available."}
                  </p>
                )}
              </section>
              <section aria-label="Execution evidence" className="space-y-2">
                <p className="text-xs font-medium">Execution evidence</p>
                <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-hint">
                  <dt className="text-muted-foreground">Current boot</dt>
                  <dd>
                    <ShortDigest value={activation.bootId} />
                  </dd>
                  <dt className="text-muted-foreground">Invocation</dt>
                  <dd>
                    <ShortDigest value={activation.invocationId} />
                  </dd>
                  <dt className="text-muted-foreground">Measured start</dt>
                  <dd className="font-mono break-words">{activation.startedAt || "unknown"}</dd>
                  <dt className="text-muted-foreground">Measured finish</dt>
                  <dd className="font-mono break-words">{activation.finishedAt || "unknown"}</dd>
                  {activation.reason && (
                    <>
                      <dt className="text-muted-foreground">Reading</dt>
                      <dd className="text-muted-foreground">{activation.reason}</dd>
                    </>
                  )}
                </dl>
              </section>
            </PopoverContent>
          </Popover>
        </div>
      </PanelBody>
    </Panel>
  )
}

function CommandStep({ command }: { command: Command }) {
  const name = command.path.split("/").pop() ?? command.path
  const product = commandProduct(command.path)
  const tone = commandTone(command)
  const outcome =
    command.exitStatus === undefined
      ? "exit outcome unknown"
      : `exit ${command.exitStatus}${command.ignoreErrors && command.exitStatus !== 0 ? ", ignored by the unit" : ""}`
  return (
    <li title={`${command.path} · ${outcome}`} className="flex min-w-0 flex-col gap-1.5">
      <span className="flex items-center gap-1.5 text-hint">
        {product ? (
          <ProductGlyph id={product} className="size-3.5" />
        ) : (
          <LogoGlyph small className="h-3.5 shrink-0 text-brand" />
        )}
        <span className="truncate font-mono">{name}</span>
      </span>
      <span aria-hidden className={cn("h-1 rounded-full", COMMAND_RULE[tone])} />
      <span className="sr-only">{outcome}</span>
    </li>
  )
}
