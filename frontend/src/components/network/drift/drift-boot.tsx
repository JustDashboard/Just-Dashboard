import { driftReading, type DriftReport } from "@/lib/network-drift"
import { relativeTime } from "@/lib/format"
import { Disclosure } from "@/components/form"
import { Panel, PanelBody, PanelHeader } from "@/components/panel"
import { Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { Digest } from "@/components/network/drift/digest"

function Fact({ label, value }: { label: string; value?: string }) {
  return (
    <div className="min-w-0 py-3">
      <dt className="text-hint text-muted-foreground">{label}</dt>
      <dd className="truncate text-body">{value || "unknown"}</dd>
    </div>
  )
}

/**
 * The unit that restores the saved configuration at boot, and the one thing
 * systemd measured about running it. The unit's three facts are plain
 * readings; the activation says how long ago it finished, ticking with the
 * page, and never claims more than the measurement: a finished activation does
 * not say it ran at boot or that reboot restoration worked, which the notice
 * under it states.
 */
export function DriftBoot({ boot }: { boot: DriftReport["boot"] }) {
  const activation = boot.execution
  const tone =
    activation.status === "succeeded"
      ? "running"
      : activation.status === "failed"
        ? "warning"
        : "unknown"
  return (
    <Panel plain>
      <PanelHeader
        title="Boot unit and measured activation"
        actions={<Status {...driftReading(boot.status)} />}
      />
      <PanelBody>
        <div className="space-y-4 text-body">
          <p className="font-mono break-words">{boot.unit}</p>
          <dl className="grid grid-cols-1 divide-y divide-hairline border-y border-hairline sm:grid-cols-3 sm:divide-x sm:divide-y-0 sm:[&>*]:px-4 sm:[&>*:first-child]:pl-0">
            <Fact label="Loaded" value={boot.loadState} />
            <Fact label="Enabled at boot" value={boot.unitFileState} />
            <Fact label="Needs daemon reload" value={boot.needDaemonReload} />
          </dl>
          {boot.reason && <p>{boot.reason}</p>}
          <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
            <Status
              tone={tone}
              label={
                activation.status === "unrecorded"
                  ? "No recorded activation"
                  : `Last activation: ${activation.status}`
              }
            />
            {activation.finishedAt && (
              <span className="text-hint text-muted-foreground">
                finished {relativeTime(activation.finishedAt)}
              </span>
            )}
          </div>
          {activation.startedAt && (
            <p>
              Measured start: <span className="font-mono">{activation.startedAt}</span>
            </p>
          )}
          {activation.finishedAt && (
            <p>
              Measured finish: <span className="font-mono">{activation.finishedAt}</span>
            </p>
          )}
          {activation.reason && <p className="text-muted-foreground">{activation.reason}</p>}
          <Notice title="Reboot attribution is unknown">
            A measured unit activation does not establish that it ran at boot or that reboot
            restoration succeeded.
          </Notice>
          <Disclosure quiet summary="Execution evidence">
            <div className="space-y-2 text-body">
              <Digest label="Current boot" value={activation.bootId} />
              <Digest label="Invocation" value={activation.invocationId} />
              {activation.commands.map((command, index) => (
                <p key={index} className="break-words">
                  <span className="font-mono">{command.path}</span>
                  {" · "}
                  {command.exitStatus === undefined
                    ? "Exit outcome unknown"
                    : `Exit ${command.exitStatus}`}
                  {command.ignoreErrors && " · Unit ignores this command's failure"}
                </p>
              ))}
              {!activation.commands.length && (
                <p className="text-muted-foreground">
                  No per-command execution outcomes are available.
                </p>
              )}
            </div>
          </Disclosure>
        </div>
      </PanelBody>
    </Panel>
  )
}
