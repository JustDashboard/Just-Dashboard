"use client"

import { useState } from "react"
import { Copy, External, Key, Play, Warning } from "@/components/icons"
import { SourceBranch, SourcePull } from "@/components/git/glyphs"
import { get, post } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentPreview,
  DeploymentPreviewApproval,
  DeploymentRunsPage,
  DeploymentTrigger,
} from "@/lib/types"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import { Field, FormFact, FormFacts, FormNote } from "@/components/form"
import { ForgeFace, ShortSha } from "@/components/git/marks"
import { Modal } from "@/components/modal"
import { Well } from "@/components/panel"
import { ProductLogo } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { VariablesPanel } from "@/components/deploy/settings/variables"
import { RunStrip } from "@/components/deploy/run-marks"
import {
  RunStatus,
  formatDuration,
  humanize,
  isActiveRun,
  runDurationSeconds,
  runTitle,
} from "@/components/deploy/vocabulary"
import type { Automation } from "@/components/deploy/settings/automation/use-automation"
import type { SourceHint } from "@/components/deploy/settings/automation/webhooks"
import { PROVIDERS } from "@/components/deploy/settings/automation/marks"

/**
 * Preview environments — a copy of the project per pull request, each built
 * only after an administrator has looked at the revision.
 *
 * Revisions waiting for that look come first, drawn as the person who opened
 * the pull request, with a fork said out loud: code from outside the
 * repository is the one fact to weigh before approving. Then the previews
 * themselves, as cards that open their own sheet, each with its runs as a
 * strip and a light round the card while one is building. A preview stopped
 * for isolation says why in the server's own sentence.
 */

function isolationOf(preview: DeploymentPreview): {
  tone: DotTone
  label: string
  note?: string
  blocked: boolean
} {
  if (preview.isolationStatus === "pending") {
    return {
      tone: "danger",
      label: "Isolation incomplete",
      blocked: true,
      note:
        "This older preview may share production resources. Deployments are blocked while its " +
        "containers and route are isolated. Cleanup retries automatically; check Docker and proxy " +
        "availability if it remains blocked.",
    }
  }
  if (preview.isolationStatus === "quarantined") {
    return {
      tone: "stopped",
      label: "Stopped for isolation",
      blocked: true,
      note:
        "The previous runtime is stopped and stored data is retained. Redeliver the pull request " +
        "event, approve its revision, and configure preview settings before deploying again.",
    }
  }
  return {
    tone: preview.state === "open" ? "running" : "stopped",
    label: humanize(preview.state),
    blocked: false,
  }
}

/**
 * Where the pull request lives on its forge, when the repository says which:
 * on the project's own host when the webhook is for the forge the project is
 * cloned from — a self-hosted GitLab is not gitlab.com — else the public one.
 */
function pullRequestUrl(trigger: DeploymentTrigger | undefined, ref: string, source: SourceHint) {
  const repository = trigger?.config.repository
  if (!trigger || !repository) return undefined
  const provider = trigger.provider || trigger.kind
  const host =
    (source.provider === provider ? source.host : undefined) ??
    PROVIDERS.find((one) => one.key === provider)?.host
  if (!host) return undefined
  switch (provider) {
    case "github":
      return `https://${host}/${repository}/pull/${ref}`
    case "gitlab":
      return `https://${host}/${repository}/-/merge_requests/${ref}`
    case "bitbucket":
      return `https://${host}/${repository}/pull-requests/${ref}`
    case "gitea":
      return `https://${host}/${repository}/pulls/${ref}`
    default:
      return undefined
  }
}

/** The address a preview answers at: its webhook's pattern with its number in. */
function previewAddress(trigger: DeploymentTrigger | undefined, ref: string) {
  const pattern = trigger?.config.previewDomain
  return pattern ? pattern.replace("{number}", ref) : undefined
}

/** Who opened the pull request, as their forge draws them; the pull request's glyph when nobody is named. */
function AuthorFace({
  login,
  trigger,
  className,
}: {
  login?: string
  trigger?: DeploymentTrigger
  className?: string
}) {
  return login ? (
    <ForgeFace login={login} provider={trigger?.provider || trigger?.kind} className={className} />
  ) : (
    <ProductLogo size="sm" fallback={SourcePull} className={className} />
  )
}

function forkOf(approval: DeploymentPreviewApproval) {
  return (
    Boolean(approval.headRepository) &&
    approval.headRepository.toLowerCase() !== approval.repository.toLowerCase()
  )
}

export function Previews({
  projectId,
  automation,
  source,
  onTurnOn,
}: {
  projectId: number
  automation: Automation
  source: SourceHint
  /** Opens Add webhook with pull-request previews already on. */
  onTurnOn: () => void
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const { approvals, previews, triggers } = automation
  const [reviewing, setReviewing] = useState<DeploymentPreviewApproval>()
  const [openId, setOpenId] = useState<number>()
  const [variablesFor, setVariablesFor] = useState<DeploymentPreview>()
  const [deploying, setDeploying] = useState<number>()
  const triggerOf = (id: number) => triggers.data?.find((trigger) => trigger.id === id)
  const approvalOf = (preview: DeploymentPreview) =>
    approvals.data?.find(
      (approval) =>
        approval.triggerId === preview.triggerId && approval.providerRef === preview.providerRef,
    )

  const deploy = async (preview: DeploymentPreview) => {
    setDeploying(preview.id)
    try {
      await post(`/deploy/${projectId}/environments/${preview.environmentId}/runs`, {
        operation: "deploy",
      })
      notify.success("Preview deployment queued")
      previews.refresh()
    } catch (error) {
      notify.error("Could not deploy preview", error)
    } finally {
      setDeploying(undefined)
    }
  }

  const reject = (approval: DeploymentPreviewApproval) =>
    confirm({
      title: `Reject PR ${approval.providerRef}`,
      confirmLabel: "Reject",
      subject: {
        mark: <AuthorFace login={approval.author} trigger={triggerOf(approval.triggerId)} />,
        name: `PR ${approval.providerRef} · ${approval.author || "unknown author"}`,
        facts: (
          <FormFact label="Revision" mono>
            {approval.revision.slice(0, 7)}
          </FormFact>
        ),
      },
      description: "This revision will not build. A later revision can still be approved.",
      action: async () => {
        await post(`/deploy/${projectId}/previews/approvals/${approval.id}/reject`, {
          revision: approval.revision,
        })
        approvals.refresh()
      },
    })

  const verbsFor = (preview: DeploymentPreview): Verb[] => {
    const isolation = isolationOf(preview)
    const url = pullRequestUrl(triggerOf(preview.triggerId), preview.providerRef, source)
    const open = preview.state === "open"
    return [
      ...(open && can("service.control")
        ? [
            {
              key: "deploy",
              label: "Deploy preview",
              detail: "Build and start this pull request's revision in its own environment.",
              icon: Play,
              inline: true,
              disabled: deploying !== undefined || isolation.blocked,
              run: () => void deploy(preview),
            },
          ]
        : []),
      ...(open && canAdmin
        ? [
            {
              key: "variables",
              label: "Variables",
              detail: "The values this preview runs with, apart from production's.",
              icon: Key,
              inline: true,
              run: () => setVariablesFor(preview),
            },
          ]
        : []),
      ...(url
        ? [
            {
              key: "pull-request",
              label: "Open pull request",
              detail: `PR ${preview.providerRef} on its forge, in a new tab.`,
              icon: External,
              run: () => void window.open(url, "_blank", "noopener"),
            },
          ]
        : []),
    ]
  }

  // Awaiting a decision, or approved but never set up — the two a reader has
  // to act on. A rejected one stays until a later revision replaces it.
  const pending = (approvals.data ?? []).filter(
    (approval) => approval.state === "pending" || !approval.configured,
  )
  const list = previews.data ?? []
  const opened = list.find((preview) => preview.id === openId)
  const previewTriggers = (triggers.data ?? []).filter((trigger) => trigger.config.preview)
  const patterns = [
    ...new Set(previewTriggers.map((trigger) => trigger.config.previewDomain).filter(Boolean)),
  ]
  const offer = canAdmin && triggers.data !== undefined && previewTriggers.length === 0

  return (
    <SettingSection
      id="previews"
      title="Preview environments"
      state={
        previewTriggers.length > 0 ? (
          <>
            {patterns.length > 0 && (
              <span className="block font-mono text-foreground/85">{patterns.join(", ")}</span>
            )}
            each revision is approved before it builds
          </>
        ) : (
          "Off — no webhook creates them"
        )
      }
      actions={
        offer &&
        list.length > 0 && (
          <Button size="sm" variant="ghost" onClick={onTurnOn}>
            <SourcePull className="size-3.5" /> Turn on previews
          </Button>
        )
      }
    >
      {approvals.error && !approvals.data && (
        <ErrorState error={approvals.error} onRetry={approvals.refresh} />
      )}
      {pending.length > 0 && (
        <section className="space-y-2">
          <GroupRule
            label="Awaiting review"
            count={pending.filter((a) => a.state === "pending").length}
          />
          <ChoiceList aria-label="Revisions awaiting review">
            {pending.map((approval) => (
              <ApprovalCard
                key={approval.id}
                approval={approval}
                trigger={triggerOf(approval.triggerId)}
                canReview={canAdmin}
                onReview={() => setReviewing(approval)}
                onReject={() => reject(approval)}
              />
            ))}
          </ChoiceList>
        </section>
      )}

      {previews.loading && !previews.data ? (
        <LoadingRows rows={2} />
      ) : previews.error && !previews.data ? (
        <ErrorState error={previews.error} onRetry={previews.refresh} />
      ) : list.length === 0 ? (
        <EmptyState
          icon={SourcePull}
          title="No preview environments"
          description="A webhook with pull-request previews creates one per pull request, after you approve its revision."
          action={
            offer && (
              <Button size="sm" variant="outline" onClick={onTurnOn}>
                Turn on previews
              </Button>
            )
          }
          className="py-8"
        />
      ) : (
        <section className="space-y-2">
          {pending.length > 0 && <GroupRule label="Environments" count={list.length} />}
          <ChoiceList aria-label="Preview environments" className="animate-rise">
            {list.map((preview) => (
              <PreviewCard
                key={preview.id}
                projectId={projectId}
                preview={preview}
                approval={approvalOf(preview)}
                trigger={triggerOf(preview.triggerId)}
                deploying={deploying === preview.id}
                verbs={verbsFor(preview)}
                onOpen={() => setOpenId(preview.id)}
              />
            ))}
          </ChoiceList>
        </section>
      )}

      <ReviewModal
        projectId={projectId}
        approval={reviewing}
        trigger={reviewing ? triggerOf(reviewing.triggerId) : undefined}
        source={source}
        onClose={() => setReviewing(undefined)}
        onApproved={(preview) => {
          setReviewing(undefined)
          approvals.refresh()
          previews.refresh()
          setVariablesFor(preview)
        }}
      />
      <PreviewSheet
        projectId={projectId}
        preview={opened}
        approval={opened ? approvalOf(opened) : undefined}
        trigger={opened ? triggerOf(opened.triggerId) : undefined}
        verbs={opened ? verbsFor(opened) : []}
        onClose={() => setOpenId(undefined)}
      />
      <SidePanel
        open={variablesFor !== undefined}
        onOpenChange={(open) => !open && setVariablesFor(undefined)}
        title={`Preview ${variablesFor?.providerRef ?? ""} variables`}
        description="Configure values used only by this preview before deploying."
        width="lg"
      >
        {variablesFor && (
          <VariablesPanel
            projectId={projectId}
            environmentId={variablesFor.environmentId}
            compact
          />
        )}
      </SidePanel>
      {dialog}
    </SettingSection>
  )
}

/**
 * One revision waiting for a decision, drawn as the person who opened it.
 * Reviewing opens the approval, so the card is lit; Reject is the one other
 * thing to do, in the card's own slot. A rejected revision stays drawn with
 * nothing to press.
 */
function ApprovalCard({
  approval,
  trigger,
  canReview,
  onReview,
  onReject,
}: {
  approval: DeploymentPreviewApproval
  trigger?: DeploymentTrigger
  canReview: boolean
  onReview: () => void
  onReject: () => void
}) {
  const rejected = approval.state === "rejected"
  const status = rejected ? (
    <Status tone="stopped" label="Rejected" />
  ) : approval.state === "pending" ? (
    <Status tone="warning" label="Awaiting approval" />
  ) : (
    <Status tone="warning" label="Setup incomplete" />
  )
  // Wide, the state sits beside the name; on a phone it leads the line of
  // facts under it, which the fork and the commit need the width of.
  const wide = useMediaQuery("(min-width: 640px)")
  return (
    <ChoiceRow
      verb={`Review revision · PR ${approval.providerRef}`}
      onSelect={onReview}
      disabled={rejected || !canReview}
      className={cn(rejected && "opacity-80")}
      leading={
        <AuthorFace
          login={approval.author}
          trigger={trigger}
          className={cn(rejected && "grayscale")}
        />
      }
      title={`PR ${approval.providerRef}`}
      description={
        <>
          {approval.author || "unknown author"} ·{" "}
          <span className="font-mono">{approval.headRepository || approval.repository}</span>
        </>
      }
      trailing={wide ? status : undefined}
      actions={
        approval.state === "pending" &&
        canReview && (
          <Button
            size="xs"
            variant="ghost"
            className="text-destructive hover:text-destructive"
            onClick={onReject}
          >
            Reject
          </Button>
        )
      }
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1.5 text-hint text-muted-foreground sm:pl-11">
        {!wide && status}
        {approval.headRef && (
          <span className="inline-flex min-w-0 items-center gap-1">
            <SourceBranch aria-hidden className="size-3 shrink-0" />
            <span className="truncate font-mono text-foreground/85">{approval.headRef}</span>
          </span>
        )}
        {forkOf(approval) && <Tag tone="warning">fork</Tag>}
        <ShortSha sha={approval.revision} />
        <span className="whitespace-nowrap">{relativeTime(approval.updatedAt)}</span>
      </div>
    </ChoiceRow>
  )
}

/**
 * Approving a revision: who wrote it, where it comes from and goes to, the
 * exact commit, and a way to read it on the forge first. Approving builds
 * nothing and deletes nothing — it lets this one revision build — so the
 * command wears the brand, not the red of a confirmation.
 */
function ReviewModal({
  projectId,
  approval,
  trigger,
  source,
  onClose,
  onApproved,
}: {
  projectId: number
  approval?: DeploymentPreviewApproval
  trigger?: DeploymentTrigger
  source: SourceHint
  onClose: () => void
  onApproved: (preview: DeploymentPreview) => void
}) {
  const [busy, setBusy] = useState(false)
  const approve = async () => {
    if (!approval) return
    setBusy(true)
    try {
      const result = await post<{ preview: DeploymentPreview }>(
        `/deploy/${projectId}/previews/approvals/${approval.id}/approve`,
        { revision: approval.revision, deploy: false },
      )
      notify.success(`PR ${approval.providerRef} approved`)
      onApproved(result.preview)
    } catch (error) {
      notify.error("Could not approve the revision", error)
    } finally {
      setBusy(false)
    }
  }
  const url = approval ? pullRequestUrl(trigger, approval.providerRef, source) : undefined
  const fork = approval ? forkOf(approval) : false
  return (
    <Modal
      open={approval !== undefined}
      onOpenChange={(open) => !open && !busy && onClose()}
      title={`Approve PR ${approval?.providerRef ?? ""}`}
      description="Allow this revision to build and run on your server."
      footer={
        <>
          <Button variant="outline" onClick={onClose} disabled={busy}>
            Cancel
          </Button>
          <Button onClick={() => void approve()} pending={busy}>
            Approve and configure
          </Button>
        </>
      }
    >
      {approval && (
        <div className="space-y-4">
          <div className="flex min-w-0 items-center gap-3">
            <AuthorFace login={approval.author} trigger={trigger} />
            <div className="min-w-0 space-y-0.5">
              <p className="truncate text-body font-medium">
                {approval.author || "Unknown author"}
              </p>
              <FormFacts>
                <FormFact label="From" mono>
                  {approval.headRepository || approval.repository}
                </FormFact>
                {approval.headRef && (
                  <FormFact label="Branch" mono>
                    {approval.headRef}
                  </FormFact>
                )}
                <FormFact label="Into" mono>
                  {approval.repository}
                </FormFact>
              </FormFacts>
            </div>
          </div>
          {fork && (
            <Notice tone="warning" icon={Warning} title="This revision comes from a fork">
              Its code was written outside {approval.repository}. Read it before it runs on this
              server.
            </Notice>
          )}
          <Field
            label="Revision"
            trailing={
              <Button
                type="button"
                size="xs"
                variant="ghost"
                onClick={() => void copyText(approval.revision, "Revision copied")}
              >
                <Copy /> Copy
              </Button>
            }
          >
            <Well className="break-all select-all">{approval.revision}</Well>
          </Field>
          {url && (
            <Button size="sm" variant="outline" asChild>
              <a href={url} target="_blank" rel="noreferrer">
                <External className="size-3.5" /> Open the pull request
              </a>
            </Button>
          )}
          <FormNote>
            It starts with empty storage and its own variables; the next revision asks again.
          </FormNote>
        </div>
      )}
    </Modal>
  )
}

/** A preview environment's last runs, polled like a backup job's. */
function usePreviewRuns(projectId: number, environmentId: number | undefined, intervalMs: number) {
  return usePoll(
    (signal) =>
      get<DeploymentRunsPage>(
        `/deploy/${projectId}/runs`,
        { view: "engine", environment: environmentId, limit: 14 },
        signal,
      ),
    intervalMs,
    [projectId, environmentId],
    { enabled: environmentId !== undefined },
  )
}

/**
 * One preview as a card that opens its sheet: its author's face, its slug,
 * the pull request and commit it runs, and the address it answers at while it
 * is open. How its last run went and whether it is isolated sit beside the
 * name when the card is wide, and lead the line under it on a phone.
 */
function PreviewCard({
  projectId,
  preview,
  approval,
  trigger,
  deploying,
  verbs,
  onOpen,
}: {
  projectId: number
  preview: DeploymentPreview
  approval?: DeploymentPreviewApproval
  trigger?: DeploymentTrigger
  deploying: boolean
  verbs: Verb[]
  onOpen: () => void
}) {
  const wide = useMediaQuery("(min-width: 640px)")
  const runs = usePreviewRuns(projectId, preview.environmentId, 15_000)
  const recent = runs.data?.runs ?? []
  const active = recent.find((run) => isActiveRun(run.state))
  const isolation = isolationOf(preview)
  const open = preview.state === "open"
  const address = open ? previewAddress(trigger, preview.providerRef) : undefined
  const readings = (
    <>
      {active ? (
        // Said as the webhook and schedule cards say theirs, with the stage.
        <TextShimmer className="pr-0.5 text-xs font-medium whitespace-nowrap">
          {`deploying · ${(active.currentStep?.label ?? "building").toLowerCase()}`}
        </TextShimmer>
      ) : (
        recent[0] && <RunStatus state={recent[0].state} />
      )}
      <Status tone={isolation.tone} label={isolation.label} />
    </>
  )
  return (
    <ChoiceRow
      verb={`Open ${preview.environmentSlug}`}
      onSelect={onOpen}
      busy={deploying || Boolean(active)}
      className={cn(!open && "opacity-80")}
      leading={<AuthorFace login={approval?.author} trigger={trigger} />}
      title={<span className="font-mono">{preview.environmentSlug}</span>}
      description={
        <>
          PR #{preview.providerRef}
          {approval?.author && ` · ${approval.author}`}
          {approval?.revision && ` · ${approval.revision.slice(0, 7)}`}
        </>
      }
      trailing={wide ? <span className="flex items-center gap-4">{readings}</span> : undefined}
      actions={
        verbs.length > 0 && (
          <VerbActions dim verbs={verbs} menuLabel={`Actions for ${preview.environmentSlug}`} />
        )
      }
    >
      <div className="space-y-2 sm:pl-11">
        <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground">
          {!wide && <span className="flex flex-wrap items-center gap-x-4 gap-y-1">{readings}</span>}
          {address && (
            <a
              href={`https://${address}`}
              target="_blank"
              rel="noreferrer"
              className="inline-flex min-w-0 items-center gap-1 font-mono text-foreground/85 underline-offset-2 hover:underline"
            >
              <span className="truncate">{address}</span>
              <External aria-hidden className="size-3 shrink-0" />
            </a>
          )}
          {recent.length > 0 && <RunStrip runs={recent} />}
          <span className="whitespace-nowrap">updated {relativeTime(preview.updatedAt)}</span>
        </div>
        {isolation.note && (
          <FormNote
            tone={isolation.tone === "danger" ? "danger" : "warning"}
            className="max-w-prose"
          >
            {isolation.note}
          </FormNote>
        )}
      </div>
    </ChoiceRow>
  )
}

/**
 * A preview's own sheet: the pull request, its author and commit, the
 * environment and its isolation as facts, the address it answers at, and its
 * runs, each opening its run page. A preview stopped for isolation says why
 * as the thing to act on.
 */
function PreviewSheet({
  projectId,
  preview,
  approval,
  trigger,
  verbs,
  onClose,
}: {
  projectId: number
  preview?: DeploymentPreview
  approval?: DeploymentPreviewApproval
  trigger?: DeploymentTrigger
  verbs: Verb[]
  onClose: () => void
}) {
  const runs = usePreviewRuns(projectId, preview?.environmentId, 5_000)
  const isolation = preview ? isolationOf(preview) : undefined
  const address =
    preview?.state === "open" ? previewAddress(trigger, preview.providerRef) : undefined
  const list = runs.data?.runs ?? []
  return (
    <SidePanel
      open={preview !== undefined}
      onOpenChange={(open) => !open && onClose()}
      title={<span className="font-mono">{preview?.environmentSlug ?? "Preview"}</span>}
      description="A pull request's own environment, its revision and its runs."
      width="md"
      actions={
        preview &&
        verbs.length > 0 && (
          <VerbBar
            verbs={verbs}
            menuLabel={`Actions for ${preview.environmentSlug} in its sheet`}
          />
        )
      }
    >
      {preview && isolation && (
        <div className="space-y-6">
          <div className="flex min-w-0 items-start gap-3">
            <AuthorFace login={approval?.author} trigger={trigger} />
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-title font-semibold tracking-tight">
                Pull request #{preview.providerRef}
              </p>
              <FormFacts>
                {approval?.author && <FormFact label="By">{approval.author}</FormFact>}
                {approval?.revision && (
                  <FormFact label="Revision" mono>
                    {approval.revision.slice(0, 12)}
                  </FormFact>
                )}
                <FormFact label="Environment">#{preview.environmentId}</FormFact>
                <FormFact label="Updated">{relativeTime(preview.updatedAt)}</FormFact>
              </FormFacts>
              {address && (
                <a
                  href={`https://${address}`}
                  target="_blank"
                  rel="noreferrer"
                  className="inline-flex items-center gap-1 font-mono text-hint text-foreground/85 underline-offset-2 hover:underline"
                >
                  {address}
                  <External aria-hidden className="size-3" />
                </a>
              )}
            </div>
            <Status tone={isolation.tone} label={isolation.label} className="shrink-0" />
          </div>
          {isolation.note && (
            <Notice
              tone={isolation.tone === "danger" ? "danger" : "warning"}
              icon={Warning}
              title={
                isolation.tone === "danger"
                  ? "Deployments are blocked"
                  : "Approve its revision again before deploying"
              }
            >
              {isolation.note}
            </Notice>
          )}
          {runs.error && !runs.data ? (
            <ErrorState error={runs.error} onRetry={runs.refresh} />
          ) : runs.loading && !runs.data ? (
            <LoadingRows rows={3} />
          ) : list.length === 0 ? (
            <FormNote>
              Nothing has run here yet — deploy the preview to build its revision.
            </FormNote>
          ) : (
            <section className="space-y-2">
              <GroupRule label="Runs" count={list.length} />
              <ChoiceList aria-label={`Runs of ${preview.environmentSlug}`}>
                {list.map((run) => (
                  <ChoiceRow
                    key={run.id}
                    href={`/deploy/${projectId}/runs/${run.id}`}
                    verb={`Open ${runTitle(run)}`}
                    busy={isActiveRun(run.state)}
                    title={runTitle(run)}
                    description={relativeTime(run.requestedAt)}
                    trailing={
                      <>
                        <RunStatus state={run.state} />
                        <span className="numeric w-16 text-right text-hint whitespace-nowrap text-muted-foreground">
                          {formatDuration(runDurationSeconds(run))}
                        </span>
                      </>
                    }
                  />
                ))}
              </ChoiceList>
            </section>
          )}
        </div>
      )}
    </SidePanel>
  )
}
