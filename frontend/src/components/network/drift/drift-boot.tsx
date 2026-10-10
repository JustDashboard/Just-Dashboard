"use client"

import { useNow } from "@/components/deploy/vocabulary"
import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { ProductGlyph, ProductLogo } from "@/components/product-logo"
import { Status } from "@/components/status-dot"
import { Notice } from "@/components/state"
import { LogoGlyph } from "@/components/logo"
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
 * The boot unit and what systemd measured of its last activation in this
 * boot. The activation is drawn as its run: each `ExecStart` command in the
 * order the unit runs it, as the tool it is — `ip`, `bridge`, `tc` and
 * `sysctl` as Linux, `nft` as netfilter — over a rule in the colour of its
 * exit: green for 0, amber for a failure the unit ignores so the other
 * domains still restore, red for one it does not, grey where systemd kept no
 * outcome. It is the deploy page's run pipeline, read from a boot.
 *
 * The finish is said as how long ago, ticking with the page, wherever the
 * timestamp's zone can be read without guessing; the raw strings stay under
 * the evidence fold either way. A measured activation still says nothing
 * about whether it ran at boot, and the page says so.
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
  return (
    <Panel plain aria-label="Boot unit">
      <PanelHeader title="Boot unit" actions={<Status {...driftReading(boot.status)} />} />
      <PanelBody className="space-y-5">
        <div className="flex min-w-0 items-center gap-3.5">
          <ProductLogo id="systemd" />
          <div className="min-w-0">
            <p className="font-mono text-body break-all">{boot.unit}</p>
            <p className="flex flex-wrap gap-x-3 gap-y-0.5 text-hint text-muted-foreground">
              {facts.map(([label, value]) => (
                <span key={label}>
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
        {boot.reason && <p className="text-body text-warning">{boot.reason}</p>}

        <section aria-label="Last activation" className="space-y-3">
          <p className="flex flex-wrap items-baseline gap-x-2 text-body">
            <span className="font-medium">
              {activation.status === "unrecorded"
                ? "No recorded activation"
                : `Last activation ${activation.status}`}
            </span>
            {finished !== undefined && (
              <time
                dateTime={new Date(finished).toISOString()}
                className="numeric text-hint text-muted-foreground"
              >
                {duration(Math.max(0, (now - finished) / 1000))} ago
              </time>
            )}
            {took !== undefined && (
              <span className="numeric text-hint text-muted-foreground">
                · ran {took < 1 ? `${Math.round(took * 1000)} ms` : `${took.toFixed(1)} s`}
              </span>
            )}
          </p>
          {activation.commands.length > 0 ? (
            <ol
              aria-label="Commands the unit ran"
              className="grid min-w-0 grid-cols-[repeat(auto-fill,minmax(3.75rem,1fr))] gap-1.5"
            >
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

        <Notice title="Reboot attribution is unknown">
          A measured unit activation does not establish that it ran at boot or that reboot
          restoration succeeded.
        </Notice>
        <Disclosure quiet summary="Execution evidence">
          <dl className="grid grid-cols-[auto_minmax(0,1fr)] gap-x-4 gap-y-1.5 text-body">
            <dt className="text-muted-foreground">Current boot</dt>
            <dd>
              <ShortDigest value={activation.bootId} />
            </dd>
            <dt className="text-muted-foreground">Invocation</dt>
            <dd>
              <ShortDigest value={activation.invocationId} />
            </dd>
            <dt className="text-muted-foreground">Measured start</dt>
            <dd className="font-mono text-hint break-words">{activation.startedAt || "unknown"}</dd>
            <dt className="text-muted-foreground">Measured finish</dt>
            <dd className="font-mono text-hint break-words">
              {activation.finishedAt || "unknown"}
            </dd>
            {activation.reason && (
              <>
                <dt className="text-muted-foreground">Reading</dt>
                <dd className="text-muted-foreground">{activation.reason}</dd>
              </>
            )}
          </dl>
        </Disclosure>
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
          <LogoGlyph className="h-3.5 shrink-0 text-brand" />
        )}
        <span className="truncate font-mono">{name}</span>
      </span>
      <span aria-hidden className={cn("h-1 rounded-full", COMMAND_RULE[tone])} />
      <span className="sr-only">{outcome}</span>
    </li>
  )
}
