"use client"

import { useState } from "react"
import Link from "next/link"
import {
  Check,
  Copy,
  GitTag,
  Inbox,
  Key,
  Lightning,
  Link as LinkGlyph,
  Logs,
  NetworkDevice,
  Pencil,
  Play,
  Plus,
  StopCircle,
  Trash,
  Warning,
  type Icon,
} from "@/components/icons"
import { SourceBranch, SourceCommit, SourcePull } from "@/components/git/glyphs"
import { del, get, post, put } from "@/lib/api"
import { hostOf } from "@/lib/clients"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useArrivals } from "@/hooks/use-arrivals"
import { useGitHubApp } from "@/hooks/use-github"
import { useMediaQuery } from "@/hooks/use-mobile"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDraftSource,
  DeploymentTrigger,
  DeploymentTriggerDelivery,
  DeploymentTriggerOutcome,
} from "@/lib/types"
import { ChoiceGrid, ProductCard } from "@/components/choice-card"
import { ChoiceList, ChoiceRow, GroupRule } from "@/components/flow"
import {
  Disclosure,
  Field,
  FieldRow,
  FormFact,
  FormFacts,
  FormNote,
  FormSection,
  OptionRow,
} from "@/components/form"
import { Modal } from "@/components/modal"
import { OutcomeStrip } from "@/components/outcome-strip"
import { Panel, Well } from "@/components/panel"
import { ProductGlyph, ProductGlyphs, ProductLogo, hostProduct } from "@/components/product-logo"
import { Row, RowList } from "@/components/row-list"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, EmptyState, ErrorState, LoadingRows, Notice } from "@/components/state"
import { Status } from "@/components/status-dot"
import { ChipCount, ChipStrip, FilterChip } from "@/components/tabs"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupInput,
  InputGroupText,
} from "@/components/ui/input-group"
import { TextShimmer } from "@/components/ui/text-shimmer"
import { Textarea } from "@/components/ui/textarea"
import { useConfirm } from "@/components/confirm-dialog"
import { VerbActions, VerbBar, type Verb } from "@/components/verbs"
import { SettingSection } from "@/components/deploy/settings/setting-card"
import { humanize, isActiveRun } from "@/components/deploy/vocabulary"
import { useProject } from "@/components/deploy/project-context"
import type { Automation } from "@/components/deploy/settings/automation/use-automation"
import {
  PROVIDERS,
  TriggerMark,
  browserZone,
  deliveriesSentence,
  deliveryDecision,
  hookUrlFor,
  providerLabel,
  triggerProduct,
  viaApp,
  zonedMoment,
  type Provider,
} from "@/components/deploy/settings/automation/marks"

/**
 * Webhooks — the senders besides the watched branch that can start a
 * deployment: a forge's push or pull request, reached through the GitHub App
 * or a signed payload URL, or anything else that signs its request.
 *
 * Each webhook is a card that opens its deliveries, drawn as the forge that
 * sends to it, with how the last delivery went beside the name in the colour
 * of what happened and its last fourteen deliveries as a strip under it — so
 * a webhook whose signature has been failing reads as that before its name is
 * read. A light runs round the card while a delivery it accepted is deploying.
 */

/** What the Add and Edit sheet is open on. */
export type WebhookTarget =
  | { mode: "add"; provider?: Provider; preview?: boolean }
  | { mode: "edit"; trigger: DeploymentTrigger }

type WebhookDraft = {
  name: string
  /** Whether the name is still the one the provider suggested. */
  named: boolean
  provider: string
  repository: string
  branch: string
  watchedPaths: string
  preview: boolean
  previewDomain: string
  viaApp: boolean
}

/** Where the project's own source lives, for a new webhook to start from. */
export type SourceHint = { provider?: string; repository?: string; ref?: string; host?: string }

export function sourceHint(
  source: DeploymentDraftSource | undefined,
  summary: { sourceRepository?: string; sourceRemote?: string; sourceRef?: string },
): SourceHint {
  return {
    provider: source?.provider || hostProduct(source?.url ?? summary.sourceRemote),
    repository: source?.repository || summary.sourceRepository,
    ref: source?.ref || summary.sourceRef,
    host: hostOf(source?.providerBaseUrl ?? source?.url ?? summary.sourceRemote ?? "") || undefined,
  }
}

function suggestedName(provider: string) {
  return provider === "generic_hook"
    ? "Signed hook"
    : `${providerLabel({ kind: provider })} webhook`
}

function blankDraft(source: SourceHint, preset: { provider?: Provider; preview?: boolean }) {
  const provider = preset.provider ?? "github"
  const own = source.provider === provider
  return {
    name: suggestedName(provider),
    named: true,
    provider,
    repository: own ? (source.repository ?? "") : "",
    branch: own ? (source.ref ?? "main") : "main",
    watchedPaths: "",
    preview: Boolean(preset.preview),
    previewDomain: "",
    viaApp: true,
  } satisfies WebhookDraft
}

function draftOf(trigger: DeploymentTrigger): WebhookDraft {
  return {
    name: trigger.name,
    named: false,
    provider: trigger.provider || trigger.kind,
    repository: trigger.config.repository ?? "",
    branch: trigger.config.ref ?? "",
    watchedPaths: (trigger.config.watchInclude ?? []).join("\n"),
    preview: Boolean(trigger.config.preview),
    previewDomain: trigger.config.previewDomain ?? "",
    viaApp: viaApp(trigger),
  }
}

/**
 * The Add and Edit sheet's state, held by the page rather than by the sheet,
 * so the picture's rings, the previews' "Turn on previews" and the section's
 * own button all open the one sheet — and so a revision bump that re-reads
 * the configuration under it never empties a half-filled form.
 */
export function useWebhookSheet(source: SourceHint) {
  const [target, setTarget] = useState<WebhookTarget>()
  const [draft, setDraft] = useState<WebhookDraft>(() => blankDraft(source, {}))
  return {
    target,
    draft,
    setDraft,
    openAdd: (preset: { provider?: Provider; preview?: boolean } = {}) => {
      setDraft(blankDraft(source, preset))
      setTarget({ mode: "add", ...preset })
    },
    openEdit: (trigger: DeploymentTrigger) => {
      setDraft(draftOf(trigger))
      setTarget({ mode: "edit", trigger })
    },
    close: () => setTarget(undefined),
  }
}

export type WebhookSheetController = ReturnType<typeof useWebhookSheet>

// ---------------------------------------------------------------------------
// The section
// ---------------------------------------------------------------------------

export function Webhooks({
  projectId,
  environmentId,
  automation,
  sheet,
  branch,
}: {
  projectId: number
  environmentId: number
  automation: Automation
  sheet: WebhookSheetController
  /** The branch the project already deploys by itself, when it watches one. */
  branch?: string
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { confirm, dialog } = useConfirm()
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const { triggers } = automation
  const [openId, setOpenId] = useState<number>()
  const [rotated, setRotated] = useState<{ name: string; secret: string }>()
  const list = triggers.data ?? []
  const opened = list.find((trigger) => trigger.id === openId)

  const toggle = async (trigger: DeploymentTrigger) => {
    try {
      await put(`${base}/triggers/${trigger.id}`, {
        name: trigger.name,
        kind: trigger.kind,
        provider: trigger.provider,
        config: trigger.config,
        enabled: !trigger.enabled,
      })
      triggers.refresh()
      notify.success(trigger.enabled ? "Webhook disabled" : "Webhook enabled")
    } catch (error) {
      notify.error("Could not update webhook", error)
    }
  }

  const remove = (trigger: DeploymentTrigger) =>
    confirm({
      title: `Remove ${trigger.name}`,
      confirmLabel: "Remove webhook",
      subject: {
        mark: <TriggerMark trigger={{ ...trigger, enabled: true }} />,
        name: trigger.name,
        facts: <SenderFacts trigger={trigger} />,
      },
      description: trigger.config.repository
        ? `Deliveries from ${trigger.config.repository} stop immediately.`
        : "Anything that signs requests to its payload URL is refused from now on.",
      action: async () => {
        await del(`${base}/triggers/${trigger.id}`)
      },
      onDone: () => {
        setOpenId(undefined)
        automation.refresh()
      },
    })

  const rotate = (trigger: DeploymentTrigger) =>
    confirm({
      title: `Rotate secret for ${trigger.name}`,
      confirmLabel: "Rotate secret",
      subject: {
        mark: <TriggerMark trigger={{ ...trigger, enabled: true }} />,
        name: trigger.name,
        facts: <SenderFacts trigger={trigger} />,
      },
      description:
        trigger.kind === "generic_hook" || trigger.kind === "api"
          ? "Whatever signs requests with the old secret is refused until it has the new one."
          : `${providerLabel(trigger)} keeps signing with the old secret until you paste the new one into its webhook; deliveries are refused until then.`,
      action: async () => {
        const result = await post<{ secret: string }>(
          `${base}/triggers/${trigger.id}/rotate-secret`,
        )
        setRotated({ name: trigger.name, secret: result.secret })
      },
    })

  const verbsFor = (trigger: DeploymentTrigger, inSheet = false): Verb[] => [
    {
      key: "toggle",
      label: trigger.enabled ? "Disable" : "Enable",
      detail: trigger.enabled
        ? "Stop accepting deliveries until re-enabled."
        : "Start accepting deliveries again.",
      icon: trigger.enabled ? StopCircle : Play,
      inline: true,
      run: () => void toggle(trigger),
    },
    ...(inSheet
      ? []
      : [
          {
            key: "deliveries",
            label: "Deliveries",
            detail: "The events this webhook received and what happened to each.",
            icon: Logs,
            run: () => setOpenId(trigger.id),
          },
        ]),
    {
      key: "edit",
      label: "Edit",
      detail: "Change its name, repository, branch, paths or previews.",
      icon: Pencil,
      run: () => sheet.openEdit(trigger),
    },
    // An App-delivered webhook has no payload URL of its own and its
    // deliveries are signed with the App's secret, so neither verb would do
    // anything for it.
    ...(viaApp(trigger)
      ? []
      : [
          {
            key: "copy",
            label: "Copy payload URL",
            detail: "The address the sender posts to, with this dashboard's origin.",
            icon: Copy,
            run: () =>
              void copyText(
                `${window.location.origin}${hookUrlFor(projectId, trigger)}`,
                "Payload URL copied",
              ),
          },
          {
            key: "rotate-secret",
            label: "Rotate secret",
            detail: "Issue a new signing secret; the old one stops working immediately.",
            icon: Key,
            run: () => rotate(trigger),
          },
        ]),
    {
      key: "remove",
      label: "Remove",
      detail: "Delete this webhook and its secret.",
      icon: Trash,
      danger: true,
      run: () => remove(trigger),
    },
  ]

  const providers = [...new Set(list.map(triggerProduct))]
  const repositories = [
    ...new Set(list.map((trigger) => trigger.config.repository).filter(Boolean)),
  ]
  const enabled = list.filter((trigger) => trigger.enabled).length

  return (
    <SettingSection
      id="webhooks"
      title="Webhooks"
      state={
        list.length > 0 ? (
          <span className="inline-flex flex-wrap items-center gap-1.5">
            <ProductGlyphs ids={providers} />
            {repositories.length > 0 ? (
              <span>
                for <span className="font-mono text-foreground/85">{repositories.join(", ")}</span>
              </span>
            ) : (
              <span>signed requests</span>
            )}
          </span>
        ) : branch ? (
          <>
            Pushes to <span className="font-mono text-foreground/85">{branch}</span> already deploy
            it — a webhook adds another sender or pull-request previews.
          </>
        ) : (
          "A webhook lets a forge or any service that signs its request start a deployment."
        )
      }
      actions={
        <>
          {list.length > 0 && (
            <span className="mr-1.5 text-hint text-muted-foreground">
              <span className="numeric text-foreground">{plural(list.length, "webhook")}</span> ·{" "}
              <span className="numeric">{enabled}</span> on
            </span>
          )}
          {canAdmin && (
            <Button size="sm" variant="outline" onClick={() => sheet.openAdd()}>
              <Plus className="size-3.5" /> Add webhook
            </Button>
          )}
        </>
      }
    >
      {triggers.loading && !triggers.data ? (
        <LoadingRows rows={2} />
      ) : triggers.error && !triggers.data ? (
        <ErrorState error={triggers.error} onRetry={triggers.refresh} />
      ) : list.length === 0 ? (
        canAdmin ? (
          <ProviderStarters onPick={(provider) => sheet.openAdd({ provider })} />
        ) : (
          <EmptyNote className="px-0 py-2 text-left">No webhooks yet.</EmptyNote>
        )
      ) : (
        <ChoiceList aria-label="Webhooks" className="animate-rise">
          {list.map((trigger) => (
            <WebhookCard
              key={trigger.id}
              trigger={trigger}
              verbs={canAdmin ? verbsFor(trigger) : undefined}
              onOpen={canAdmin ? () => setOpenId(trigger.id) : undefined}
            />
          ))}
        </ChoiceList>
      )}

      <DeliveriesSheet
        projectId={projectId}
        base={base}
        trigger={opened}
        verbs={opened ? verbsFor(opened, true) : []}
        onClose={() => setOpenId(undefined)}
      />
      <Modal
        open={Boolean(rotated)}
        onOpenChange={(next) => !next && setRotated(undefined)}
        title={rotated ? `New secret for ${rotated.name}` : "New secret"}
        description="The new signing secret, shown once."
        footer={<Button onClick={() => setRotated(undefined)}>Done</Button>}
      >
        {rotated && (
          <div className="space-y-4">
            <Notice
              tone="warning"
              icon={Warning}
              title={`Copy the new secret for ${rotated.name} now`}
            >
              It is not shown again. Deliveries signed with the old one are refused from now on.
            </Notice>
            <SecretField label="Secret" value={rotated.secret} copied="Secret copied" />
          </div>
        )}
      </Modal>
      {dialog}
    </SettingSection>
  )
}

/**
 * The five senders, for a project that has none yet: pressing one opens the
 * sheet with it chosen. The same cards the sheet draws, so the empty section
 * is the first step of the form rather than a sentence about it.
 */
function ProviderStarters({ onPick }: { onPick: (provider: Provider) => void }) {
  return (
    <div className="space-y-2">
      <p className="text-hint text-muted-foreground">Add one for</p>
      <ChoiceGrid columns="compact" className="xl:grid-cols-3">
        {PROVIDERS.map((provider) => (
          <ProductCard
            key={provider.key}
            product={provider.key === "generic_hook" ? "webhook" : provider.key}
            label={provider.label}
            detail={provider.key === "generic_hook" ? "anything that signs" : "push · pull request"}
            onClick={() => onPick(provider.key)}
          />
        ))}
      </ChoiceGrid>
    </div>
  )
}

/** Repository, branch and how deliveries arrive — a webhook's line of facts. */
function SenderFacts({ trigger }: { trigger: DeploymentTrigger }) {
  return (
    <>
      {trigger.config.repository && (
        <FormFact label="Repository" mono>
          {trigger.config.repository}
        </FormFact>
      )}
      {trigger.config.ref && (
        <FormFact label="Branch" mono>
          {trigger.config.ref}
        </FormFact>
      )}
      <DeliveryFact trigger={trigger} />
    </>
  )
}

function DeliveryFact({ trigger }: { trigger: DeploymentTrigger }) {
  return (
    <FormFact label="Delivery">{viaApp(trigger) ? "GitHub App" : "Signed payload URL"}</FormFact>
  )
}

/**
 * One webhook as a card that opens its deliveries. The two readings — how
 * the last delivery went, and whether it is on — sit beside the name when
 * the card is wide and lead the line under it on a phone; chosen once
 * (`useMediaQuery`) rather than drawn twice, so each is in the page once.
 */
function WebhookCard({
  trigger,
  verbs,
  onOpen,
}: {
  trigger: DeploymentTrigger
  verbs?: Verb[]
  onOpen?: () => void
}) {
  const project = useProject()
  const wide = useMediaQuery("(min-width: 640px)")
  const runId = trigger.lastDelivery?.runId
  const deploying =
    runId === undefined
      ? undefined
      : project.runs.find((run) => run.id === runId && isActiveRun(run.state))
  const readings = (
    <>
      {deploying ? (
        <TextShimmer className="pr-0.5 text-xs font-medium whitespace-nowrap">
          {`deploying #${deploying.runNumber}`}
        </TextShimmer>
      ) : (
        <LastDelivery trigger={trigger} />
      )}
      <Status
        tone={trigger.enabled ? "running" : "stopped"}
        label={trigger.enabled ? "Enabled" : "Disabled"}
      />
    </>
  )
  const include = trigger.config.watchInclude ?? []
  const forge = trigger.kind !== "generic_hook" && trigger.kind !== "api"
  return (
    <ChoiceRow
      verb={`Open ${trigger.name}`}
      onSelect={onOpen}
      disabled={!onOpen}
      busy={Boolean(deploying)}
      className={cn(!trigger.enabled && "opacity-80")}
      leading={<TriggerMark trigger={trigger} />}
      title={trigger.name}
      description={
        <>
          {trigger.config.repository ? (
            <span className="font-mono">{trigger.config.repository}</span>
          ) : (
            providerLabel(trigger)
          )}
          {trigger.config.ref && (
            <>
              <SourceBranch aria-hidden className="mx-1 inline size-3 align-[-2px]" />
              <span className="font-mono">{trigger.config.ref}</span>
            </>
          )}
          {viaApp(trigger) ? " · via GitHub App" : " · signed hook"}
        </>
      }
      trailing={wide ? <span className="flex items-center gap-4">{readings}</span> : undefined}
      actions={verbs && <VerbActions dim verbs={verbs} menuLabel={`Actions for ${trigger.name}`} />}
    >
      <div className="flex min-w-0 flex-wrap items-center gap-x-6 gap-y-2 text-hint text-muted-foreground sm:pl-11">
        {!wide && <span className="flex flex-wrap items-center gap-x-4 gap-y-1">{readings}</span>}
        <DeliveryStrip recent={trigger.recent} />
        {forge && (
          <span className="inline-flex min-w-0 items-center gap-1.5">
            {include.length === 0 ? (
              "all paths"
            ) : (
              <>
                <span className="truncate font-mono text-foreground/85">{include[0]}</span>
                {include.length > 1 && <span className="numeric">+{include.length - 1}</span>}
              </>
            )}
          </span>
        )}
        {trigger.config.preview && (
          <span className="inline-flex min-w-0 items-center gap-1.5">
            <SourcePull aria-hidden className="size-3.5 shrink-0" />
            <span className="truncate">
              previews
              {trigger.config.previewDomain && (
                <>
                  {" at "}
                  <span className="font-mono text-foreground/85">
                    {trigger.config.previewDomain}
                  </span>
                </>
              )}
            </span>
          </span>
        )}
      </div>
    </ChoiceRow>
  )
}

const LAST_WORD: Record<string, string> = {
  accepted: "accepted",
  suppressed: "ignored",
  rejected: "refused",
}

/**
 * How the last delivery went, in its colour. An administrator's list carries
 * the delivery itself; anyone else's only the trigger's own last status and
 * time, which say the same thing less exactly.
 */
function LastDelivery({ trigger }: { trigger: DeploymentTrigger }) {
  const decision = trigger.lastDelivery?.decision ?? trigger.lastStatus
  const at = trigger.lastDelivery?.receivedAt ?? trigger.lastDeliveryAt
  if (!decision)
    return (
      <span className="text-xs whitespace-nowrap text-muted-foreground">no deliveries yet</span>
    )
  const word = LAST_WORD[decision] ?? humanize(decision).toLowerCase()
  return (
    <Status
      tone={deliveryDecision(decision).tone}
      label={at ? `${word} ${relativeTime(at)}` : word}
    />
  )
}

/**
 * A webhook's last deliveries, oldest first. Only an administrator's list
 * carries them — the delivery log is theirs alone — so for anyone else there
 * is simply no strip rather than an error in the card.
 */
function DeliveryStrip({
  recent,
}: {
  recent?: Pick<DeploymentTriggerOutcome, "decision" | "reason" | "receivedAt">[]
}) {
  if (!recent || recent.length === 0) return null
  return (
    <OutcomeStrip
      label={deliveriesSentence(recent.map((outcome) => outcome.decision))}
      items={recent.map((outcome, index) => {
        const decision = deliveryDecision(outcome.decision)
        return {
          key: `${outcome.receivedAt}-${index}`,
          tone: decision.strip,
          title: [decision.label, outcome.reason, relativeTime(outcome.receivedAt)]
            .filter(Boolean)
            .join(" · "),
        }
      })}
    />
  )
}

// ---------------------------------------------------------------------------
// Deliveries
// ---------------------------------------------------------------------------

const EVENT: Record<string, { word: string; glyph: Icon }> = {
  push: { word: "Push", glyph: SourceCommit },
  pull_request: { word: "Pull request", glyph: SourcePull },
  merge_request: { word: "Merge request", glyph: SourcePull },
  create: { word: "Create", glyph: GitTag },
  tag: { word: "Tag", glyph: GitTag },
  tag_push: { word: "Tag", glyph: GitTag },
  ping: { word: "Ping", glyph: NetworkDevice },
}

function eventOf(event: string) {
  return EVENT[event] ?? { word: humanize(event), glyph: Lightning }
}

/** `refs/heads/main` → main, `refs/tags/v1` → v1, `refs/pull/42/head` → #42. */
function shortRef(ref: string | undefined) {
  if (!ref) return undefined
  const pull = ref.match(/^refs\/(?:pull|merge-requests)\/(\d+)\//)
  if (pull) return `#${pull[1]}`
  return ref.replace(/^refs\/(heads|tags)\//, "")
}

/** The day a delivery arrived, spelled as the schedule sheet spells its firings. */
function dayOf(iso: string) {
  const day = (date: Date) =>
    new Date(date.getFullYear(), date.getMonth(), date.getDate()).getTime()
  const days = Math.round((day(new Date()) - day(new Date(iso))) / 86_400_000)
  return days === 0 ? "Today" : days === 1 ? "Yesterday" : zonedMoment(iso, browserZone()).day
}

type DecisionFilter = "all" | "accepted" | "suppressed" | "rejected"

const FILTERS: { key: DecisionFilter; label: string }[] = [
  { key: "all", label: "All" },
  { key: "accepted", label: "Accepted" },
  { key: "suppressed", label: "Ignored" },
  { key: "rejected", label: "Refused" },
]

/** How to make the sender deliver something, for a webhook that has heard nothing yet. */
function redeliverStep(trigger: DeploymentTrigger) {
  const ref = trigger.config.ref ?? "the branch"
  if (viaApp(trigger)) return `Push to ${ref} or open a pull request — the App delivers both.`
  switch (trigger.provider || trigger.kind) {
    case "github":
      return `Push to ${ref}, or redeliver an event from the repository's Settings → Webhooks → Recent deliveries.`
    case "gitlab":
      return `Push to ${ref}, or press Test → Push events on the project's Settings → Webhooks.`
    case "bitbucket":
      return `Push to ${ref}, or resend a request from Repository settings → Webhooks → View requests.`
    case "gitea":
      return `Push to ${ref}, or press Test Delivery on the repository's Settings → Webhooks.`
    default:
      return "POST a signed request to the payload URL above."
  }
}

/**
 * A webhook's own sheet: what it is — the sender, the repository and branch,
 * how deliveries arrive and, for a signed hook, the address to post to —
 * then its deliveries as a strip, counted by what was decided, and one by one
 * under the day they arrived. A delivery that started a run opens it.
 */
function DeliveriesSheet({
  projectId,
  base,
  trigger,
  verbs,
  onClose,
}: {
  projectId: number
  base: string
  trigger?: DeploymentTrigger
  verbs: Verb[]
  onClose: () => void
}) {
  const deliveries = usePoll(
    (signal) =>
      get<DeploymentTriggerDelivery[]>(
        `${base}/triggers/${trigger?.id}/deliveries`,
        undefined,
        signal,
      ),
    10000,
    [base, trigger?.id],
    { enabled: Boolean(trigger) },
  )
  const log = deliveries.data ?? []

  return (
    <SidePanel
      open={Boolean(trigger)}
      onOpenChange={(open) => !open && onClose()}
      title={trigger ? `Deliveries · ${trigger.name}` : "Deliveries"}
      description="Recent events this webhook received and what happened to each."
      width="md"
      actions={
        trigger && <VerbBar verbs={verbs} menuLabel={`Actions for ${trigger.name} in its sheet`} />
      }
    >
      {trigger && (
        <div className="space-y-6">
          <div className="flex min-w-0 items-start gap-3">
            <TriggerMark trigger={trigger} />
            <div className="min-w-0 flex-1 space-y-1">
              <p className="text-title font-semibold tracking-tight">{providerLabel(trigger)}</p>
              <FormFacts>
                <SenderFacts trigger={trigger} />
              </FormFacts>
            </div>
          </div>
          {!viaApp(trigger) && (
            <SecretField
              label="Payload URL"
              value={`${window.location.origin}${hookUrlFor(projectId, trigger)}`}
              copied="Payload URL copied"
            />
          )}

          {deliveries.error && !deliveries.data ? (
            <ErrorState error={deliveries.error} onRetry={deliveries.refresh} />
          ) : deliveries.loading && !deliveries.data ? (
            <LoadingRows rows={4} />
          ) : log.length === 0 ? (
            <EmptyState
              icon={Inbox}
              title="No deliveries yet"
              description={redeliverStep(trigger)}
            />
          ) : (
            // One per webhook: the filter chosen on one is not carried to the
            // next, and a log that has just been read has not "arrived".
            <DeliveryLog key={trigger.id} projectId={projectId} log={log} />
          )}
        </div>
      )}
    </SidePanel>
  )
}

/**
 * A webhook's deliveries once read: the strip, the filters counted by what
 * was decided, and the rows under the day they arrived. Only a delivery a
 * later poll brings rises.
 */
function DeliveryLog({ projectId, log }: { projectId: number; log: DeploymentTriggerDelivery[] }) {
  const project = useProject()
  const [filter, setFilter] = useState<DecisionFilter>("all")
  const arrived = useArrivals(log.map((delivery) => delivery.deliveryId))
  const zone = browserZone()
  const shown = filter === "all" ? log : log.filter((delivery) => delivery.decision === filter)
  const days: { label: string; rows: DeploymentTriggerDelivery[] }[] = []
  for (const delivery of shown) {
    const label = dayOf(delivery.receivedAt)
    const day = days.at(-1)
    if (day?.label === label) day.rows.push(delivery)
    else days.push({ label, rows: [delivery] })
  }
  const count = (decision: DecisionFilter) =>
    decision === "all" ? log.length : log.filter((one) => one.decision === decision).length

  return (
    <div className="animate-rise space-y-6">
      <div className="space-y-3">
        <DeliveryStrip recent={[...log].reverse()} />
        {/* The sheet's body pads 16px, not the page's 20. */}
        <ChipStrip role="group" aria-label="Show deliveries" className="max-sm:-mx-4 max-sm:px-4">
          {FILTERS.map((item) => (
            <FilterChip
              key={item.key}
              selected={filter === item.key}
              onClick={() => setFilter(item.key)}
            >
              {item.label} <ChipCount>{count(item.key)}</ChipCount>
            </FilterChip>
          ))}
        </ChipStrip>
      </div>
      {shown.length === 0 ? (
        <EmptyNote className="px-0 text-left">None of the last {log.length}.</EmptyNote>
      ) : (
        <Panel plain className="space-y-4">
          {days.map((day) => (
            <section key={day.label} className="space-y-1">
              <GroupRule label={day.label} count={day.rows.length} />
              <RowList aria-label={`Deliveries, ${day.label}`}>
                {day.rows.map((delivery) => {
                  const event = eventOf(delivery.event)
                  const decision = deliveryDecision(delivery.decision)
                  const run = project.runs.find((one) => one.id === delivery.runId)
                  const ref = shortRef(delivery.ref)
                  return (
                    <Row
                      key={delivery.deliveryId}
                      className={cn(
                        "flex-wrap",
                        arrived.has(delivery.deliveryId) && "animate-rise",
                      )}
                      leading={<ProductLogo size="sm" fallback={event.glyph} />}
                      title={ref ? `${event.word} · ${ref}` : event.word}
                      subtitle={[delivery.reason, zonedMoment(delivery.receivedAt, zone).time]
                        .filter(Boolean)
                        .join(" · ")}
                      trailing={
                        <>
                          {/* On a phone the decision is the row's last word,
                              so every one ends at the same edge. */}
                          <Status
                            tone={decision.tone}
                            label={decision.label}
                            className="max-sm:order-last"
                          />
                          {/* From sm, a slot of its own whether or not there is a
                              run, so the decisions stand in one column. */}
                          <span className="flex w-28 justify-end max-sm:w-auto">
                            {delivery.runId !== undefined && (
                              <Button size="xs" variant="outline" asChild>
                                <Link href={`/deploy/${projectId}/runs/${delivery.runId}`}>
                                  {run ? `View run #${run.runNumber}` : "View run"}
                                </Link>
                              </Button>
                            )}
                          </span>
                        </>
                      }
                    />
                  )
                })}
              </RowList>
            </section>
          ))}
        </Panel>
      )}
    </div>
  )
}

/**
 * A value to copy: a secret shown once, a payload URL. The value is its own
 * element in a well, selectable in one press, with Copy at the label's end.
 */
function SecretField({ label, value, copied }: { label: string; value: string; copied: string }) {
  return (
    <Field
      label={label}
      trailing={
        <Button
          type="button"
          size="xs"
          variant="ghost"
          onClick={() => void copyText(value, copied)}
        >
          <Copy /> Copy
        </Button>
      }
    >
      <Well className="break-all select-all">{value}</Well>
    </Field>
  )
}

// ---------------------------------------------------------------------------
// Add and Edit
// ---------------------------------------------------------------------------

/** Where to paste, on each forge — plain lines, in the order they are done. */
function pasteSteps(provider: string): string[] {
  switch (provider) {
    case "github":
      return [
        "Open the repository's Settings → Webhooks → Add webhook.",
        "Paste the payload URL, set Content type to application/json and paste the secret.",
        "Choose Pushes and Pull requests, then Add webhook.",
      ]
    case "gitlab":
      return [
        "Open the project's Settings → Webhooks → Add new webhook.",
        "Paste the payload URL, and the secret into Secret token.",
        "Tick Push events and Merge request events, then Add webhook.",
      ]
    case "bitbucket":
      return [
        "Open Repository settings → Webhooks → Add webhook.",
        "Paste the payload URL and the secret.",
        "Choose Repository push and Pull request created and updated, then Save.",
      ]
    case "gitea":
      return [
        "Open the repository's Settings → Webhooks → Add webhook → Gitea.",
        "Paste the payload URL, set Content type to application/json and paste the secret.",
        "Choose Push events and Pull request events, then Add webhook.",
      ]
    default:
      return [
        "Sign each request's body with HMAC-SHA256, keyed with the secret.",
        "Send the hex digest in an X-JD-Signature-256 header, as sha256=<digest>.",
        "POST the request to the payload URL.",
      ]
  }
}

/** The address a preview gets, drawn from the pattern as it is typed. */
function previewExample(pattern: string) {
  const host = pattern.trim()
  if (!host.includes("{number}")) return undefined
  return host.replace("{number}", "42")
}

/**
 * Adding a webhook, or editing one.
 *
 * The sender is picked by its mark from five cards, not read out of a
 * list of five words; the repository and branch are one field each with the
 * forge's host and the branch glyph inside their edge; and the pull-request
 * option says, as it is typed, what address PR #42 would get. A new signed
 * webhook turns the sheet into its result — the payload URL, the secret shown
 * once, and where on the forge each goes — rather than a banner on the page
 * behind it. Editing opens on the webhook itself and keeps its sender.
 */
export function WebhookSheet({
  projectId,
  environmentId,
  sheet,
  source,
  onSaved,
}: {
  projectId: number
  environmentId: number
  sheet: WebhookSheetController
  source: SourceHint
  onSaved: () => void
}) {
  const base = `/deploy/${projectId}/environments/${environmentId}`
  const { target, draft, setDraft } = sheet
  const editing = target?.mode === "edit" ? target.trigger : undefined
  const [saving, setSaving] = useState(false)
  const [error, setError] = useState<string>()
  const [created, setCreated] = useState<{ provider: string; url: string; secret: string }>()
  // Read with the page rather than when the sheet opens, so whether GitHub
  // delivers through the App is known before Create can be pressed.
  const githubApp = useGitHubApp()
  const app = githubApp.data?.configured ? githubApp.data : undefined
  const patch = (next: Partial<WebhookDraft>) => setDraft((prev) => ({ ...prev, ...next }))
  const forge = draft.provider !== "generic_hook" && draft.provider !== "api"
  // An edit keeps the delivery it was made with: the sheet cannot change it,
  // and an App status still loading or since unconfigured is not a reason to
  // turn an App webhook into a signed one that nothing sends to.
  const deliversViaApp = editing
    ? viaApp(editing)
    : draft.provider === "github" && Boolean(app) && draft.viaApp
  const meta = PROVIDERS.find((one) => one.key === draft.provider)
  // The project's own forge first: a self-hosted GitLab is not gitlab.com.
  const host = (source.provider === draft.provider ? source.host : undefined) ?? meta?.host
  const ownRepository =
    source.provider === draft.provider &&
    Boolean(source.repository) &&
    draft.repository.trim() === source.repository
  const example = previewExample(draft.previewDomain)
  const invalid =
    !draft.name.trim() ||
    (forge && (!draft.repository.trim() || !draft.branch.trim())) ||
    (!editing && draft.provider === "github" && githubApp.loading)

  const close = () => {
    setCreated(undefined)
    setError(undefined)
    sheet.close()
  }

  const save = async () => {
    setSaving(true)
    setError(undefined)
    const watchInclude = draft.watchedPaths
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
    const config = {
      ...(editing?.config ?? {}),
      repository: forge ? draft.repository.trim() : undefined,
      ref: forge ? draft.branch.trim() : undefined,
      watchInclude: watchInclude.length > 0 ? watchInclude : undefined,
      preview: forge && draft.preview,
      previewDomain: forge && draft.preview ? draft.previewDomain.trim() || undefined : undefined,
      delivery: editing ? editing.config.delivery : deliversViaApp ? ("app" as const) : undefined,
    }
    try {
      if (editing) {
        await put(`${base}/triggers/${editing.id}`, {
          name: draft.name.trim(),
          kind: editing.kind,
          provider: editing.provider,
          config,
          enabled: editing.enabled,
        })
        notify.success("Webhook saved")
        onSaved()
        close()
        return
      }
      const result = await post<{ trigger: DeploymentTrigger; secret: string }>(
        `${base}/triggers`,
        {
          name: draft.name.trim(),
          kind: draft.provider,
          provider: draft.provider,
          enabled: true,
          config,
        },
      )
      onSaved()
      if (viaApp(result.trigger)) {
        notify.success("Webhook created", {
          description: "Pushes and pull requests reach it through the GitHub App.",
        })
        close()
      } else {
        notify.success("Webhook created")
        setCreated({
          provider: draft.provider,
          url: hookUrlFor(projectId, result.trigger),
          secret: result.secret,
        })
      }
    } catch (caught) {
      setError(caught instanceof Error ? caught.message : String(caught))
    } finally {
      setSaving(false)
    }
  }

  const title = created ? "Add webhook" : editing ? `Edit ${editing.name}` : "Add webhook"

  return (
    <SidePanel
      open={Boolean(target) || Boolean(created)}
      onOpenChange={(open) => !open && !saving && close()}
      title={title}
      description={
        editing
          ? "Change what this webhook deploys and when."
          : "Add a sender that can start a deployment, or turn on pull request previews."
      }
      width="md"
      footer={
        created ? (
          <Button onClick={close}>Done</Button>
        ) : (
          <>
            <FormNote className="mr-auto basis-full sm:basis-auto">
              {deliversViaApp
                ? `Nothing to paste into GitHub`
                : editing
                  ? "Its payload URL and secret stay as they are"
                  : "Signed with a secret shown once"}
            </FormNote>
            <Button variant="outline" onClick={close} disabled={saving}>
              Cancel
            </Button>
            <Button onClick={() => void save()} disabled={invalid} pending={saving}>
              {editing ? "Save webhook" : "Add webhook"}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <CreatedStep created={created} />
      ) : (
        <div className="space-y-6" aria-busy={saving}>
          {editing && (
            // The repository and branch are the fields below, so the head
            // names only what the sheet cannot change.
            <div className="flex min-w-0 items-center gap-3">
              <TriggerMark trigger={editing} />
              <div className="min-w-0 space-y-0.5">
                <p className="truncate text-body font-medium">{providerLabel(editing)}</p>
                <FormFacts>
                  <DeliveryFact trigger={editing} />
                </FormFacts>
              </div>
            </div>
          )}
          <Field label="Name" htmlFor="webhook-name">
            <Input
              id="webhook-name"
              value={draft.name}
              onChange={(event) => patch({ name: event.target.value, named: false })}
            />
          </Field>

          {!editing && (
            <FormSection title="Sender">
              <ChoiceGrid
                columns="compact"
                role="group"
                aria-label="Provider"
                className="xl:grid-cols-3"
              >
                {PROVIDERS.map((provider) => (
                  <ProductCard
                    key={provider.key}
                    product={provider.key === "generic_hook" ? "webhook" : provider.key}
                    fallback={LinkGlyph}
                    label={provider.label}
                    detail={
                      provider.key === "generic_hook"
                        ? "anything that signs"
                        : (provider.host ??
                          (source.provider === "gitea" ? source.host : "self-hosted"))
                    }
                    selected={draft.provider === provider.key}
                    onClick={() => {
                      const own = source.provider === provider.key
                      patch({
                        provider: provider.key,
                        name: draft.named ? suggestedName(provider.key) : draft.name,
                        repository:
                          own && !draft.repository ? (source.repository ?? "") : draft.repository,
                      })
                    }}
                  />
                ))}
              </ChoiceGrid>
              {draft.provider === "github" && app && (
                <OptionRow
                  title="Deliver through the GitHub App"
                  hint={
                    <span className="inline-flex flex-wrap items-center gap-1.5">
                      <ProductGlyph id="github" />
                      {app.app?.name ?? "The App"}
                      {app.installations.length > 0 &&
                        ` · installed on ${app.installations.map((one) => one.account).join(", ")}`}
                    </span>
                  }
                  checked={draft.viaApp}
                  onCheckedChange={(next) => patch({ viaApp: next })}
                />
              )}
            </FormSection>
          )}

          {forge && (
            <FormSection title="Repository">
              <FieldRow>
                <Field
                  label="Repository"
                  htmlFor="webhook-repository"
                  hint={ownRepository ? "This project's repository" : "owner/repository"}
                >
                  <InputGroup>
                    <InputGroupAddon>
                      <ProductGlyph id={draft.provider} />
                      {host && <InputGroupText className="max-sm:hidden">{host}/</InputGroupText>}
                    </InputGroupAddon>
                    <InputGroupInput
                      id="webhook-repository"
                      placeholder="owner/repository"
                      value={draft.repository}
                      onChange={(event) => patch({ repository: event.target.value })}
                      className="font-mono sm:text-xs"
                      spellCheck={false}
                      autoComplete="off"
                    />
                  </InputGroup>
                </Field>
                <Field label="Branch" htmlFor="webhook-branch">
                  <InputGroup>
                    <InputGroupAddon>
                      <SourceBranch aria-hidden />
                    </InputGroupAddon>
                    <InputGroupInput
                      id="webhook-branch"
                      value={draft.branch}
                      onChange={(event) => patch({ branch: event.target.value })}
                      className="font-mono sm:text-xs"
                      spellCheck={false}
                      autoComplete="off"
                    />
                  </InputGroup>
                </Field>
              </FieldRow>
            </FormSection>
          )}

          {forge && (
            <FormSection title="What it deploys">
              <Field
                label="Watched paths"
                htmlFor="webhook-paths"
                hint="One glob per line. Leave empty to watch everything."
              >
                <Textarea
                  id="webhook-paths"
                  placeholder="services/api/**"
                  rows={3}
                  value={draft.watchedPaths}
                  onChange={(event) => patch({ watchedPaths: event.target.value })}
                  className="font-mono sm:text-xs"
                  spellCheck={false}
                />
              </Field>
              <TryAChange
                base={base}
                include={draft.watchedPaths}
                exclude={editing?.config.watchExclude}
              />
            </FormSection>
          )}

          {forge && (
            <FormSection title="Pull requests">
              <OptionRow
                title="Create isolated environments for pull requests"
                hint="Each revision waits for an administrator's approval before it builds."
                checked={draft.preview}
                onCheckedChange={(next) => patch({ preview: next })}
              >
                <div className="space-y-3">
                  <FormNote>
                    A preview&rsquo;s variables and storage are its own, never production&rsquo;s. A
                    container preview gets a network of its own; Compose and workloads that reach
                    the host need a separate plan.
                  </FormNote>
                  <Field
                    label="Preview address"
                    htmlFor="webhook-preview-domain"
                    hint={
                      example
                        ? `PR #42 → ${example} · point its DNS at this server`
                        : "Use {number} where the pull request's number goes."
                    }
                  >
                    <InputGroup>
                      <InputGroupAddon>
                        <InputGroupText>https://</InputGroupText>
                      </InputGroupAddon>
                      <InputGroupInput
                        id="webhook-preview-domain"
                        placeholder="pr-{number}.example.com"
                        value={draft.previewDomain}
                        onChange={(event) => patch({ previewDomain: event.target.value })}
                        className="font-mono sm:text-xs"
                        spellCheck={false}
                        autoComplete="off"
                      />
                    </InputGroup>
                  </Field>
                </div>
              </OptionRow>
            </FormSection>
          )}

          {!forge && (
            <FormNote>
              Anything that can sign its request with the secret can start a deployment: a CI job, a
              registry, a script.
            </FormNote>
          )}
          {error && (
            <FormNote tone="danger" role="alert">
              {error}
            </FormNote>
          )}
        </div>
      )}
    </SidePanel>
  )
}

/**
 * A few changed paths against the watched ones, answered by the server's
 * own matcher — so "would a change to the docs deploy it?" is asked here
 * rather than found out on the next push. Asked on a press, not as it is
 * typed: each answer is written to the audit log.
 */
function TryAChange({
  base,
  include,
  exclude,
}: {
  base: string
  include: string
  /** The saved excludes an edit keeps, which the server matches against as well. */
  exclude?: string[]
}) {
  const [paths, setPaths] = useState("")
  const [answer, setAnswer] = useState<{ asked: string; matched: boolean }>()
  const [asking, setAsking] = useState(false)
  const lines = (text: string) =>
    text
      .split("\n")
      .map((line) => line.trim())
      .filter(Boolean)
  const asked = `${paths}\u0000${include}`
  const ask = async () => {
    setAsking(true)
    try {
      const result = await post<{ matched: boolean }>(`${base}/watch-paths/simulate`, {
        changedPaths: lines(paths),
        include: lines(include),
        exclude,
      })
      setAnswer({ asked, matched: result.matched })
    } catch (error) {
      notify.error("Could not check the paths", error)
    } finally {
      setAsking(false)
    }
  }
  const current = answer?.asked === asked ? answer : undefined
  return (
    <Disclosure quiet summary="Try a change">
      <div className="space-y-2">
        <Field
          label="Changed paths"
          htmlFor="webhook-try-paths"
          hint="One per line, as a commit lists them."
        >
          <Textarea
            id="webhook-try-paths"
            rows={2}
            placeholder="docs/readme.md"
            value={paths}
            onChange={(event) => setPaths(event.target.value)}
            className="font-mono sm:text-xs"
            spellCheck={false}
          />
        </Field>
        <div className="flex flex-wrap items-center gap-3">
          <Button
            type="button"
            size="sm"
            variant="outline"
            disabled={lines(paths).length === 0}
            pending={asking}
            onClick={() => void ask()}
          >
            Check
          </Button>
          {current && (
            <FormNote tone={current.matched ? "success" : "default"} className="animate-rise">
              {current.matched ? (
                <>
                  <Check aria-hidden className="mr-1 inline size-3 align-[-1px]" />
                  Would deploy
                </>
              ) : (
                "Would be ignored — no watched path changed"
              )}
            </FormNote>
          )}
        </div>
      </div>
    </Disclosure>
  )
}

/**
 * A new signed webhook's result: the one thing to act on (copy the secret),
 * the two values, and where on the forge each goes, as plain lines.
 */
function CreatedStep({ created }: { created: { provider: string; url: string; secret: string } }) {
  const url = `${window.location.origin}${created.url}`
  const provider = providerLabel({ kind: created.provider })
  return (
    <div className="space-y-5">
      <Notice tone="warning" icon={Warning} title="Copy this secret now">
        It is shown once. Rotate it from the webhook&rsquo;s menu if it is lost.
      </Notice>
      <SecretField label="Payload URL" value={url} copied="Payload URL copied" />
      <SecretField label="Secret" value={created.secret} copied="Secret copied" />
      <FormSection
        title={
          <span className="inline-flex items-center gap-2">
            <ProductGlyph id={triggerProduct({ kind: created.provider })} className="size-4" />
            {created.provider === "generic_hook" ? "From the sender" : `On ${provider}`}
          </span>
        }
      >
        <ol className="space-y-1.5 text-body leading-relaxed text-muted-foreground">
          {pasteSteps(created.provider).map((step) => (
            <li key={step}>{step}</li>
          ))}
        </ol>
      </FormSection>
    </div>
  )
}
