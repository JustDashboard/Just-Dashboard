"use client"

import { Check, Copy } from "@/components/icons"
import { get } from "@/lib/api"
import { plural } from "@/lib/format"
import type { ContainerDetail, DeploymentRuntimeService } from "@/lib/types"
import { useCopy } from "@/hooks/use-copy"
import { usePoll } from "@/hooks/use-poll"
import { FormSection } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { ErrorState, LoadingRows } from "@/components/state"
import { Status } from "@/components/status-dot"
import { stateWord } from "@/components/docker/container-cells"
import { shortDigest } from "@/components/deploy/runtime-model"

/** `oomKilled` is omitted by the server when false, and the shared type predates it. */
type Detail = ContainerDetail & { oomKilled?: boolean }

/**
 * What Docker records about one container of the release, for the facts the
 * card has no room for: the image it really runs, how often it has been
 * restarted, how it last ended, who it runs as and what it is joined to.
 *
 * A `SidePanel`, because the card is a click target that opens the container
 * and a disclosure inside it would be a second thing to press on a row that
 * already has one; the panel is drawn by the page and not by the card, since
 * a press inside a portal reaches the row it was opened from. Nothing is read
 * until it is open, and what is read is the container's own page's request,
 * so the two cannot disagree. Environment variables are not here: they are
 * redacted on the way out, and the variables page owns them.
 */
export function ServiceDetails({
  service,
  product,
  onClose,
}: {
  service?: DeploymentRuntimeService
  product: string
  onClose: () => void
}) {
  const open = service !== undefined
  // The state is a dependency so a restart pressed with the panel open
  // brings the new restart count rather than the one it opened with.
  const result = usePoll<Detail>(
    (signal) =>
      get<Detail>(
        `/docker/containers/${encodeURIComponent(service?.containerId ?? "")}`,
        undefined,
        signal,
      ),
    0,
    [service?.containerId, service?.state],
    { enabled: open },
  )
  const detail = result.data
  const name = service?.name || service?.containerId

  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => !next && onClose()}
      title={
        <>
          <ProductLogo id={product} size="sm" />
          <span className="min-w-0 truncate">{name}</span>
        </>
      }
      description="What Docker records about this container: its image, restarts, last exit, user and networks."
      width="sm"
    >
      {result.error ? (
        <ErrorState error={result.error} onRetry={result.refresh} />
      ) : !detail ? (
        <LoadingRows />
      ) : (
        <div className="animate-rise space-y-6">
          <FormSection title="Image">
            <Facts>
              <Fact label="Reference" mono>
                {detail.image}
              </Fact>
              <Fact label="Digest" mono>
                <Digest id={detail.imageId} />
              </Fact>
            </Facts>
          </FormSection>

          <FormSection title="Lifecycle">
            <Facts>
              <Fact label="State">
                <Status
                  state={detail.state}
                  label={stateWord(detail.state)}
                  className="text-body font-normal"
                />
              </Fact>
              <Fact label="Restarts">
                {detail.restartCount === 0 ? "Never" : plural(detail.restartCount, "time")}
              </Fact>
              {detail.state !== "running" && (
                <Fact label="Exit code" mono>
                  {detail.exitCode}
                </Fact>
              )}
              <Fact label="Out of memory">
                {detail.oomKilled ? (
                  <Status tone="danger" label="Killed by the kernel" className="text-body" />
                ) : (
                  "No"
                )}
              </Fact>
              <Fact label="Restart policy">{detail.restartPolicy || "none"}</Fact>
              {detail.error && <Fact label="Docker's error">{detail.error}</Fact>}
            </Facts>
          </FormSection>

          <FormSection title="Process">
            <Facts>
              <Fact label="User" mono={Boolean(detail.user)}>
                {detail.user || <Muted>image default</Muted>}
              </Fact>
              <Fact label="Working directory" mono={Boolean(detail.workingDir)}>
                {detail.workingDir || <Muted>image default</Muted>}
              </Fact>
              <Fact label="Entrypoint" mono={detail.entrypoint.length > 0}>
                {detail.entrypoint.length > 0 ? (
                  detail.entrypoint.join(" ")
                ) : (
                  <Muted>image default</Muted>
                )}
              </Fact>
            </Facts>
          </FormSection>

          <FormSection title="Networks" hint={detail.networkMode && `mode ${detail.networkMode}`}>
            {detail.networkDetails.length === 0 ? (
              <p className="text-hint text-muted-foreground">Not joined to a network.</p>
            ) : (
              <Facts>
                {detail.networkDetails.map((network) => (
                  <Fact key={network.networkId || network.name} label={network.name} mono>
                    {network.ipAddress || <Muted>no address</Muted>}
                    {network.aliases.length > 0 && (
                      <span className="ml-2 text-hint text-muted-foreground">
                        {network.aliases.join(", ")}
                      </span>
                    )}
                  </Fact>
                ))}
              </Facts>
            )}
          </FormSection>
        </div>
      )}
    </SidePanel>
  )
}

function Facts({ children }: { children: React.ReactNode }) {
  return <dl className="divide-y divide-hairline">{children}</dl>
}

function Fact({
  label,
  mono,
  children,
}: {
  label: string
  /** The value is a literal from the host — an address, a path, a number. */
  mono?: boolean
  children: React.ReactNode
}) {
  return (
    <div className="flex min-w-0 items-baseline gap-4 py-2">
      <dt className="w-36 shrink-0 truncate text-hint text-muted-foreground" title={label}>
        {label}
      </dt>
      <dd
        className={mono ? "min-w-0 flex-1 font-mono text-xs break-all" : "min-w-0 flex-1 text-body"}
      >
        {children}
      </dd>
    </div>
  )
}

function Muted({ children }: { children: React.ReactNode }) {
  return <span className="font-sans text-body text-muted-foreground">{children}</span>
}

/** The short digest, and the whole one on the clipboard. */
function Digest({ id }: { id: string }) {
  const { copy, copied } = useCopy()
  const short = shortDigest(id)
  if (!short) return <Muted>unknown</Muted>
  return (
    <span className="inline-flex items-center gap-1.5">
      <span title={id}>sha256:{short}</span>
      <IconAction
        label={copied ? "Digest copied" : "Copy the full digest"}
        onClick={() => void copy(id)}
        className="size-5"
      >
        {copied ? <Check /> : <Copy />}
      </IconAction>
    </span>
  )
}
