"use client"

import { useRef, useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import {
  ArrowUpRight,
  Copy,
  CornerUpRight,
  External,
  Globe,
  Key,
  LockClosed,
  LockOpen,
  Plus,
  RefreshClockwise,
  Trash,
  Warning,
} from "@/components/icons"
import { ApiError, get, put, refusedIndex } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { usePoll } from "@/hooks/use-poll"
import type {
  DeploymentDomainRoute,
  DeploymentEnvironmentConfiguration,
  DeploymentErrorPage,
  DeploymentForwardAuth,
  DeploymentHostnameSuggestion,
  DeploymentMaintenanceSwitch,
  DeploymentPageName,
  DeploymentRedirectCode,
  DeploymentRouteHSTS,
  DeploymentTailnetAddress,
  Exposure,
  ForwardAuthProvider,
} from "@/lib/types"
import { SSO_PROVIDERS, chooseSsoProvider, ssoPreset } from "@/lib/sso-providers"
import { Field, FieldCheck, FieldRow, FormNote, OptionList, OptionRow } from "@/components/form"
import { Well } from "@/components/panel"
import { ProductGlyph, ProductLogo, issuerProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { HttpsToggle } from "@/components/deploy/https-toggle"
import { Tag } from "@/components/tag"
import { CertificateStatus, ROUTE_LABEL, ROUTE_TONE, hostOf } from "@/components/deploy/vocabulary"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import { OwnershipSelect } from "@/components/deploy/settings/mounts"
import { useProject } from "@/components/deploy/project-context"
import {
  HSTS_DEFAULT_MAX_AGE,
  HSTS_MAX_AGES,
  HSTS_PRELOAD_MAX_AGE,
  HeaderListField,
  ListField,
} from "@/components/proxy/site-form"
import { PAGE_LABEL, PageEditor } from "@/components/proxy/page-editor"
import { useConfirm } from "@/components/confirm-dialog"
import {
  hstsOffered,
  hstsPolicyState,
  routeAccessState,
  savedHSTS,
  signInConflicts,
  withPreload,
} from "@/components/deploy/settings/route-access"
import {
  readTailnetEntry,
  tailnetAccessState,
  tailnetBlockers,
  tailnetRuntime,
  tailnetSwitchClears,
} from "@/components/deploy/settings/tailnet-access"
import {
  ERROR_PAGES,
  errorPagesState,
  maintenancePolicyState,
  maintenanceState,
  maintenanceUnready,
  retryAfterOf,
  routeMaintenanceOf,
} from "@/components/deploy/settings/route-pages"

/**
 * Domains — the hostnames this environment answers on.
 *
 * It was a framed card holding a framed box per hostname, each a bag of
 * labelled controls whose labels sat on two baselines, with the certificate
 * as two grey status lines and the port, bind address and two links in a
 * footnote under the card. Now each hostname is one row:
 * the certificate's issuer drawn as itself (Let's Encrypt, from the issuer the
 * live release's certificate names), the name as a link to the site, its
 * route and certificate as readings with the days left, then the two
 * binaries it carries as one segmented control and who owns it. The site and
 * the certificate behind it are verbs in the row's menu rather than
 * underlined links in a footnote.
 *
 * A hostname only ever arrives through "Add domain", which is the one place
 * the certificate check happens; an existing row is never retyped, only
 * toggled, reassigned or removed, so the sheet always describes a name the
 * operator is about to commit to rather than one already half-edited.
 *
 * The hostname is the largest word in its row, a rung above the toggles'
 * labels, because it is what the row is; the readings under it share one
 * size. What changes when a toggle is pressed — the issuer's mark, the route
 * and certificate readings, a row arriving or leaving — rises into its new
 * state rather than being repainted in place.
 */

type DomainValue = DeploymentEnvironmentConfiguration["domains"][number]

/** A hostname is made by the project or linked to it; it is never only watched. */
const DOMAIN_OWNERSHIP: DomainValue["ownership"][] = ["managed", "linked"]

/**
 * The answers a redirecting name can give, permanent first. 308 and 307 keep
 * a form's POST a POST; 301 and 302 are for clients older than that.
 */
const REDIRECT_CODES: [DeploymentRedirectCode, string][] = [
  [308, "Permanent · 308"],
  [301, "Permanent · 301"],
  [307, "Temporary · 307"],
  [302, "Temporary · 302"],
]

export function DomainsSettings({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const state = useConfiguration(projectId, environmentId)
  return (
    <SettingsPage state={state}>
      {(configuration) => (
        <>
          {/* A tailnet-only environment has no public route, so it has no
              domains, and none of the rules below can be saved beside it. */}
          {!configuration.runtime.tailnet && (
            <DomainsForm configuration={configuration} save={state.save} />
          )}
          <TailnetForm configuration={configuration} save={state.save} />
          {/* Rules for a route that does not exist yet would be a form with
              nothing to act on; one already saved stays editable. */}
          {(configuration.domains.length > 0 ||
            Boolean(configuration.runtime.allowFrom?.length) ||
            Boolean(configuration.runtime.responseHeaders?.length) ||
            Boolean(configuration.runtime.forwardAuth) ||
            Boolean(configuration.runtime.hsts)) && (
            <RouteRulesForm configuration={configuration} save={state.save} />
          )}
          {(configuration.domains.length > 0 ||
            Boolean(configuration.runtime.maintenance) ||
            Boolean(configuration.runtime.errorPages?.length)) && (
            <>
              <MaintenanceForm projectId={projectId} environmentId={environmentId} />
              <RoutePagesForm
                configuration={configuration}
                save={state.save}
                projectId={projectId}
                environmentId={environmentId}
              />
            </>
          )}
        </>
      )}
    </SettingsPage>
  )
}

type TailnetDraft = { on: boolean; allow: string[] }

/**
 * Tailnet only — the environment answers on this server's tailnet address
 * and nowhere else, the way a pull request preview does: tailscale serve
 * maps one port of the tailnet to it, with the tailnet's own certificate and
 * no public domain. Beside the public domains, because it is the other answer
 * to the question the page asks: where does this environment answer.
 *
 * Who may open it is the list under the switch, Tailscale logins and tags,
 * each read as the server reads it before it is added. Empty is the whole
 * tailnet. A list puts a gate in front of the application that asks the
 * dashboard about every request, and the dashboard asks tailscaled.
 *
 * Turning it on takes away, with the same save, what only a public route
 * has — the domains, the sign-in, the address list, the headers — and says
 * so before the save, rather than leaving the server to refuse it one field
 * at a time.
 */
function TailnetForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const project = useProject()
  // A preview's plan is a copy of the environment it previews: its list is
  // set there, and a change here would be undone at the next head.
  const preview = Boolean(configuration.runtime.previewIsolation)
  const canAdmin = can("system.admin") && !preview
  const savedTailnet = configuration.runtime.tailnet
  const saved: TailnetDraft = { on: Boolean(savedTailnet), allow: savedTailnet?.allow ?? [] }
  const draft = useSettingDraft(`deploy.${project.projectId}.settings.tailnet`, saved)
  const value = draft.value
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const live = project.operations?.tailnet
  const clears = value.on && !savedTailnet ? tailnetSwitchClears(configuration) : []
  const blockers = value.on ? tailnetBlockers(configuration.runtime) : []

  const onSave = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await save({
        ...(value.on ? { domains: [] } : {}),
        runtime: (latest) => tailnetRuntime(latest.runtime, value.on ? value.allow : undefined),
      })
      return true
    } catch (error) {
      if (error instanceof ApiError && error.field?.startsWith("runtime.tailnet")) {
        setRefusal(error.message)
      } else {
        notify.error("Could not save the tailnet setting", error)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Tailnet"
      onSave={onSave}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canAdmin}
      onDiscard={() => {
        setRefusal(undefined)
        draft.discard()
      }}
    >
      <SettingSection
        title="Tailnet"
        state={tailnetAccessState(value.on ? { allow: value.allow } : undefined)}
        status={settingStatus({ dirty: draft.dirty, refused: Boolean(refusal) })}
      >
        <OptionList>
          <OptionRow
            title="Tailnet only"
            hint="Answers on this server's tailnet address and nowhere else, as pull request previews do: no public domain, no public certificate, and only devices on your tailnet reach it."
            checked={value.on}
            disabled={!canAdmin}
            onCheckedChange={(on) => draft.set({ ...value, on })}
          >
            {value.on && (
              <div className="flex flex-col gap-4">
                <TailnetAddress live={live} saved={Boolean(savedTailnet)} />
                <TailnetAllowField
                  values={value.allow}
                  onChange={(allow) => draft.set({ ...value, allow })}
                  disabled={!canAdmin}
                />
                <FormNote>
                  {value.allow.length > 0
                    ? "Every request is checked first: the dashboard asks Tailscale who holds the address it came from, and anyone not listed is answered 403. The application is told X-Tailnet-Login and X-Tailnet-User, and never what the visitor sent."
                    : "The tailnet is the fence: everyone on it can open the application, as with a preview. List logins or tags to admit only them."}
                </FormNote>
                {preview && (
                  <FormNote>A preview takes this from the environment it previews.</FormNote>
                )}
                {clears.length > 0 && (
                  <FormNote tone="warning">
                    Saving also takes away {clears.join(", ")}: a tailnet-only environment has no
                    public route.
                  </FormNote>
                )}
                {blockers.map((blocker) => (
                  <FormNote key={blocker} tone="warning">
                    It cannot be tailnet-only while {blocker}. Change that under{" "}
                    <Link
                      href={`/deploy/${project.projectId}/settings/runtime`}
                      className="underline underline-offset-4"
                    >
                      Runtime
                    </Link>{" "}
                    first.
                  </FormNote>
                ))}
                {refusal && (
                  <FormNote role="alert" tone="danger">
                    {refusal}
                  </FormNote>
                )}
              </div>
            )}
          </OptionRow>
        </OptionList>
      </SettingSection>
    </SettingForm>
  )
}

/**
 * The address the environment has on the tailnet, as its live release has
 * it: a link with Tailscale's mark, or that it gets one on the next
 * deployment — the port is chosen then and kept.
 */
function TailnetAddress({
  live,
  saved,
}: {
  live: DeploymentTailnetAddress | undefined
  saved: boolean
}) {
  const host = live?.url ? hostOf(live.url) : undefined
  const [tone, label]: [DotTone, string] = !saved
    ? ["notice", "Not saved yet"]
    : !host
      ? ["notice", "Gets its address on the next deployment"]
      : live?.published
        ? ["running", "Published on the tailnet"]
        : ["warning", "Not reachable · redeploy"]
  return (
    <div className="flex min-w-0 items-center gap-3">
      <ProductLogo size="sm" id="tailscale" fallback={Globe} />
      <div className="min-w-0 space-y-1">
        {host ? (
          <a
            href={live?.url}
            target="_blank"
            rel="noreferrer"
            aria-label={`Open ${host}`}
            className="flex max-w-full min-w-0 items-center gap-1.5 rounded-sm font-mono text-sm font-medium focus-ring hover:underline"
          >
            <span className="truncate">{host}</span>
            <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
          </a>
        ) : (
          <p className="text-sm font-medium text-muted-foreground">Address on the tailnet</p>
        )}
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <Status key={label} tone={tone} label={label} className="animate-rise" />
          {live?.gate && live.allow.length > 0 && (
            <span className="text-muted-foreground">
              {live.gate === "caddy"
                ? "Checked by the dashboard's own Caddy on loopback"
                : "Checked by this server's nginx"}
            </span>
          )}
        </div>
      </div>
    </div>
  )
}

/**
 * Who may open the environment: Tailscale logins and tags, each read the
 * way the server reads it as it is typed, so an entry that would be refused
 * never reaches the list. A login is kept in lower case, since providers do
 * not agree on one; a tag as written, since the tailnet's policy compares
 * tags exactly.
 */
function TailnetAllowField({
  values,
  onChange,
  disabled,
}: {
  values: string[]
  onChange: (values: string[]) => void
  disabled: boolean
}) {
  const [typed, setTyped] = useState("")
  const [problem, setProblem] = useState<string>()
  const add = () => {
    const read = readTailnetEntry(typed, values)
    if ("error" in read) {
      setProblem(read.error)
      return
    }
    onChange([...values, read.entry])
    setTyped("")
    setProblem(undefined)
  }
  return (
    <Field
      label="Who may open it"
      htmlFor="tailnet-allow"
      hint="Tailscale logins and tags. Empty lets everyone on the tailnet in."
      info="A login is the account a person signs in to Tailscale with, alice@example.com or alice@github. A tag is a machine's ACL tag, tag:ci; a tagged machine is admitted by its tags alone."
      error={problem}
    >
      <div className="space-y-2">
        {values.length > 0 && (
          <ul aria-label="Admitted on the tailnet" className="flex flex-wrap items-center gap-1.5">
            {values.map((value) => (
              <li key={value}>
                <Tag mono className="gap-1.5 pr-0.5">
                  {value}
                  {!disabled && (
                    <IconAction
                      type="button"
                      label={`Remove ${value}`}
                      size="icon-xs"
                      className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                      onClick={() => onChange(values.filter((entry) => entry !== value))}
                    >
                      <Trash />
                    </IconAction>
                  )}
                </Tag>
              </li>
            ))}
          </ul>
        )}
        {!disabled && (
          <div className="flex items-center gap-2">
            <Input
              id="tailnet-allow"
              value={typed}
              onChange={(event) => {
                setTyped(event.target.value)
                setProblem(undefined)
              }}
              onKeyDown={(event) => {
                if (event.key === "Enter") {
                  event.preventDefault()
                  add()
                }
              }}
              placeholder="alice@example.com or tag:ci"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={Boolean(problem)}
              className="font-mono text-xs"
            />
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={add}
              disabled={!typed.trim()}
              aria-label="Add to who may open it"
            >
              <Plus className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
    </Field>
  )
}

function DomainsForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  const router = useRouter()
  const draft = useSettingDraft(
    `deploy.${project.projectId}.settings.domains`,
    configuration.domains,
  )
  const domains = draft.value
  const setDomains = draft.set
  const [saving, setSaving] = useState(false)
  const [adding, setAdding] = useSessionState(
    `deploy.${project.projectId}.settings.domains.adding`,
    false,
  )
  const [rowError, setRowError] = useState<{ index: number; message: string }>()
  const observed =
    project.operations?.domains.status === "available" ? project.operations.domains : undefined
  const routes = observed?.domains
  // Which names the live release carries, read from its own list whether or
  // not Proxy could be asked about them — without Proxy the routes are
  // unknown, but the names are still the release's. A read that failed before
  // listing any leaves liveness unknown, which is never "just added".
  const liveList = project.operations?.domains
  const liveKnown = Boolean(
    liveList && (liveList.status === "available" || liveList.domains.length > 0),
  )
  const liveNames = new Set(liveList?.domains.map((route) => route.hostname.toLowerCase()))

  const routeFor = (hostname: string) =>
    routes?.find((route) => route.hostname.toLowerCase() === hostname.toLowerCase())
  const { bindAddress } = configuration.runtime
  const publicBind = bindAddress === "0.0.0.0" || bindAddress === "::"

  const update = (index: number, next: Partial<DomainValue>) =>
    setDomains(domains.map((item, i) => (i === index ? { ...item, ...next } : item)))

  const persist = async (next: DomainValue[]) => {
    await save({ domains: next })
    notify.success("Domains saved")
  }

  const onSave = async () => {
    setSaving(true)
    setRowError(undefined)
    try {
      await save({ domains })
      return true
    } catch (error) {
      const index = error instanceof ApiError ? refusedIndex(error.field, "domains") : undefined
      if (error instanceof ApiError && index !== undefined) {
        setRowError({ index, message: error.message })
      } else {
        notify.error("Could not save domains", error)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  // A name the live release still routes but the draft no longer has: it
  // stops answering once a deployment carries the change, and until then it
  // is still being served — so it stays on the page, struck through.
  const saved = new Set(configuration.domains.map((domain) => domain.hostname.toLowerCase()))
  const drafted = new Set(domains.map((domain) => domain.hostname.toLowerCase()))
  const leaving = (routes ?? []).filter(
    (route) => route.route === "served" && !drafted.has(route.hostname.toLowerCase()),
  )

  const verbsFor = (domain: DomainValue, index: number): Verb[] => {
    const route = routeFor(domain.hostname)
    const verbs: Verb[] = []
    if (route?.deepLink)
      verbs.push({
        key: "site",
        label: "Open the serving site",
        icon: Globe,
        run: () => router.push(route.deepLink!),
      })
    if (route?.certificateLink)
      verbs.push({
        key: "certificate",
        label: "Open the certificate",
        icon: LockClosed,
        run: () => router.push(route.certificateLink!),
      })
    const target = servedNames(domains).find((name) => name !== domain.hostname)
    if (canAdmin && !domain.redirectTo && target)
      verbs.push({
        key: "redirect",
        label: "Redirect to another domain",
        icon: CornerUpRight,
        run: () => update(index, { redirectTo: target, redirectCode: undefined }),
      })
    if (canAdmin && domain.redirectTo)
      verbs.push({
        key: "serve",
        label: "Serve the application here",
        icon: Globe,
        run: () => update(index, { redirectTo: undefined, redirectCode: undefined }),
      })
    if (canAdmin)
      verbs.push({
        key: "remove",
        label: "Remove",
        icon: Trash,
        danger: true,
        // A name redirecting to the one removed would point nowhere, so it
        // goes back to serving the application rather than failing the save.
        run: () =>
          setDomains(
            domains
              .filter((_, i) => i !== index)
              .map((item) =>
                item.redirectTo === domain.hostname
                  ? { ...item, redirectTo: undefined, redirectCode: undefined }
                  : item,
              ),
          ),
      })
    return verbs
  }

  return (
    <>
      <SettingForm
        name="Domains"
        onSave={onSave}
        dirty={draft.dirty}
        changes={draft.changes}
        saving={saving}
        canEdit={canAdmin}
        onDiscard={() => {
          setRowError(undefined)
          draft.discard()
        }}
      >
        <SettingSection
          title="Domains"
          state={
            observed?.siteName && (
              // Which proxy answers is the host's, nginx or Caddy; the site is
              // this environment's own, named by its id.
              <Link
                href={`/proxy/sites?site=${encodeURIComponent(observed.siteName)}`}
                className="flex min-w-0 items-center gap-1 rounded-sm font-mono focus-ring transition-colors hover:text-foreground hover:underline"
              >
                <span className="truncate">{observed.siteName}</span>
                <ArrowUpRight aria-hidden className="size-3 shrink-0" />
              </Link>
            )
          }
          status={settingStatus({ dirty: draft.dirty, refused: Boolean(rowError) })}
          actions={
            canAdmin && (
              <Button type="button" size="sm" variant="outline" onClick={() => setAdding(true)}>
                <Plus className="size-3.5" /> Add domain
              </Button>
            )
          }
        >
          {publicBind && (
            <Notice tone="warning" title="This environment binds a public address" icon={Warning}>
              Traffic on {bindAddress} reaches the container directly, ahead of Proxy. Make sure the{" "}
              <Link href="/network/firewall" className="underline underline-offset-4">
                firewall
              </Link>{" "}
              allows only the traffic you expect.
            </Notice>
          )}
          {domains.length === 0 && leaving.length === 0 ? (
            <EmptyState
              icon={Globe}
              title="No public domains yet"
              description="Add a hostname to route its traffic here through Proxy, with a certificate issued on the first deployment."
            />
          ) : (
            <ul aria-label="Domains" className="@container divide-y divide-hairline">
              {domains.map((domain, index) => (
                // Keyed on the name alone (the server refuses a duplicate),
                // so removing one row does not remount every row after it and
                // make each one rise again.
                <DomainRow
                  key={domain.hostname}
                  index={index}
                  domain={domain}
                  route={routeFor(domain.hostname)}
                  pending={
                    liveKnown &&
                    configuration.pending.pending &&
                    !liveNames.has(domain.hostname.toLowerCase())
                  }
                  unsaved={!saved.has(domain.hostname.toLowerCase())}
                  canAdmin={canAdmin}
                  error={rowError?.index === index ? rowError.message : undefined}
                  targets={servedNames(domains).filter((name) => name !== domain.hostname)}
                  verbs={verbsFor(domain, index)}
                  onChange={(next) => update(index, next)}
                />
              ))}
              {leaving.map((route) => {
                const removed = saved.has(route.hostname.toLowerCase())
                  ? "Removed · not saved"
                  : "Removed · stops on the next deployment"
                return (
                  <li
                    key={`leaving-${route.hostname}`}
                    className="flex min-w-0 animate-rise items-center gap-3 py-3 first:pt-0"
                  >
                    <ProductLogo size="sm" fallback={LockOpen} className="opacity-60" />
                    <span className="min-w-0 flex-1 truncate font-mono text-sm text-muted-foreground line-through">
                      {route.hostname}
                    </span>
                    {/* Saving turns "not saved" into when it stops; the word
                        rises into its new state rather than being repainted. */}
                    <Status key={removed} tone="warning" label={removed} className="animate-rise" />
                  </li>
                )
              })}
            </ul>
          )}
        </SettingSection>
      </SettingForm>

      <AddDomainPanel
        open={adding}
        onOpenChange={setAdding}
        unsaved={draft.changes}
        onAdd={async (domain) => {
          await persist([...domains, domain])
          setAdding(false)
        }}
      />
    </>
  )
}

/**
 * One hostname. Wide, it is one line: the issuer's mark, the name with its
 * route and certificate under it, the two binaries it carries as one
 * segmented control, who owns it, and its menu. On a phone the controls take
 * a second line of their own at the full width, and the menu stays pinned to
 * the name — one grid reflowed, so nothing is drawn twice.
 */
function DomainRow({
  domain,
  route,
  pending,
  unsaved,
  canAdmin,
  error,
  index,
  targets,
  verbs,
  onChange,
}: {
  domain: DomainValue
  route?: DeploymentDomainRoute
  pending: boolean
  unsaved: boolean
  canAdmin: boolean
  error?: string
  index: number
  /** The other names that serve the application, which this one may redirect to. */
  targets: string[]
  verbs: Verb[]
  onChange: (next: Partial<DomainValue>) => void
}) {
  const host = domain.hostname
  const protection = domain.protection
  const scheme = domain.https ? "https" : "http"
  const [routeTone, routeLabel]: [DotTone, string] = unsaved
    ? ["notice", "Not saved yet"]
    : route
      ? [ROUTE_TONE[route.route], ROUTE_LABEL[route.route]]
      : pending
        ? ["notice", "Added · routes on the next deployment"]
        : ["unknown", "Not observed"]
  // The certificate the live route has is only this row's answer while the
  // row still asks for what the route serves. Turn HTTPS on or off and the
  // live answer is about to stop being true, so the row says what the next
  // deployment does instead of contradicting its own toggle.
  // A name served through Cloudflare Tunnel has no certificate here at all:
  // Cloudflare's edge terminates its HTTPS, and the route serves it plain to
  // the connector, so the live certificate readings say nothing about it.
  const tunnel = Boolean(domain.tunnel)
  const httpsChanging = !tunnel && Boolean(route) && domain.https !== route?.https
  const liveCertificate = route && !unsaved && !httpsChanging && !tunnel ? route : undefined

  const issuer = tunnel ? "cloudflare" : issuerProduct(liveCertificate?.certificateIssuer)

  const setProtection = (next: Partial<NonNullable<DomainValue["protection"]>>) =>
    protection && onChange({ protection: { ...protection, ...next } })

  const nextScheme = domain.https ? "Certificate on deploy" : "HTTP only on deploy"

  return (
    <li className="grid min-w-0 animate-rise grid-cols-[2rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-3 py-3 first:pt-0 @min-[40rem]:grid-cols-[2rem_minmax(0,1fr)_auto_11rem_auto]">
      {/* Keyed on what it draws, so pressing HTTPS swaps the issuer for the
          open lock by rising into it rather than repainting in place. */}
      <ProductLogo
        key={issuer ?? (domain.https ? "https" : "http")}
        size="sm"
        id={issuer}
        fallback={domain.https ? LockClosed : LockOpen}
        className="animate-rise"
      />
      <div className="min-w-0 space-y-1">
        <a
          href={`${scheme}://${host}/`}
          target="_blank"
          rel="noreferrer"
          aria-label={`Open ${host}`}
          className="flex max-w-full min-w-0 items-center gap-1.5 rounded-sm font-mono text-sm font-medium focus-ring hover:underline"
        >
          <span className="truncate">{host}</span>
          <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
        </a>
        {/* One size along the line: the days left and who renews it were
            11px beside 12px readings, two baselines for one row of facts. */}
        <div className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1 text-xs">
          <Status key={routeLabel} tone={routeTone} label={routeLabel} className="animate-rise" />
          {domain.redirectTo && (
            <span
              key={domain.redirectTo}
              className="flex min-w-0 animate-rise items-center gap-1 text-muted-foreground"
            >
              <CornerUpRight aria-hidden className="size-3 shrink-0" />
              <span className="truncate">
                Redirects to <span className="font-mono">{domain.redirectTo}</span>
              </span>
            </span>
          )}
          {/* Each name is served on its own scheme: an HTTPS name sends its
              plain-HTTP visitors on, a plain one is served as asked, whatever
              the other names on the route do — so the row says which. */}
          {!domain.redirectTo && (
            <span key={tunnel ? "tunnel" : scheme} className="animate-rise text-muted-foreground">
              {tunnel
                ? "Through Cloudflare Tunnel · HTTPS at Cloudflare's edge"
                : domain.https
                  ? "Plain HTTP is redirected to HTTPS"
                  : "Plain HTTP, no redirect"}
            </span>
          )}
          {/* The issuer is the row's mark, so the line carries the state and the
              days left and not the issuer's glyph a second time. */}
          {route && !unsaved && httpsChanging && (
            <Status key={nextScheme} tone="notice" label={nextScheme} className="animate-rise" />
          )}
          {/* One group, so turning HTTPS back on brings the live certificate's
              readings back together rather than popping in one by one. */}
          {liveCertificate && (
            <span className="flex min-w-0 animate-rise flex-wrap items-center gap-x-3 gap-y-1">
              <CertificateStatus domain={liveCertificate} />
              {typeof liveCertificate.certificateDaysLeft === "number" &&
                liveCertificate.certificateDaysLeft > 0 && (
                  <span
                    className={cn(
                      "numeric",
                      liveCertificate.certificate === "expiring"
                        ? "text-warning"
                        : "text-muted-foreground",
                    )}
                  >
                    {plural(liveCertificate.certificateDaysLeft, "day")} left
                  </span>
                )}
              {liveCertificate.certificateRenewedBy === "caddy" && (
                <span className="text-muted-foreground">Renewed by Caddy</span>
              )}
            </span>
          )}
        </div>
      </div>
      <div className="col-span-full flex min-w-0 flex-wrap items-center gap-2 pl-11 @min-[40rem]:contents">
        <ToggleGroup
          type="multiple"
          variant="outline"
          value={[domain.https && "https", protection && "password", tunnel && "tunnel"].filter(
            (value): value is string => Boolean(value),
          )}
          onValueChange={(values) => {
            // Through the tunnel is HTTPS by definition — Cloudflare's edge
            // answers the visitor — so turning it on turns HTTPS on with it.
            const through = values.includes("tunnel")
            onChange({
              https: through || values.includes("https"),
              protection: values.includes("password")
                ? (protection ?? { username: "", password: "" })
                : undefined,
              tunnel: through || undefined,
            })
          }}
          disabled={!canAdmin}
          className="shrink-0"
        >
          <ToggleGroupItem
            value="https"
            aria-label={`HTTPS on ${host}`}
            // Held on, not greyed out: the name is HTTPS, at Cloudflare's edge.
            disabled={tunnel}
            className={cn(
              "h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9",
              tunnel && canAdmin && "disabled:cursor-default disabled:opacity-100",
            )}
          >
            <LockClosed
              className={cn(
                "size-3.5 transition-colors",
                domain.https ? "text-brand" : "text-muted-foreground",
              )}
            />
            HTTPS
          </ToggleGroupItem>
          <ToggleGroupItem
            value="password"
            aria-label={`Password on ${host}`}
            className="h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9"
          >
            <Key
              className={cn(
                "size-3.5 transition-colors",
                protection ? "text-brand" : "text-muted-foreground",
              )}
            />
            Password
          </ToggleGroupItem>
          {/* A wildcard is not routed through a tunnel: each name is a route
              and a record of its own at Cloudflare. */}
          {(tunnel || !host.startsWith("*.")) && (
            <ToggleGroupItem
              value="tunnel"
              aria-label={`Cloudflare Tunnel on ${host}`}
              className="h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9"
            >
              <ProductGlyph
                id="cloudflare"
                className={cn("transition-opacity", !tunnel && "opacity-50 grayscale")}
              />
              Tunnel
            </ToggleGroupItem>
          )}
        </ToggleGroup>
        <OwnershipSelect
          value={domain.ownership}
          onChange={(ownership) => onChange({ ownership })}
          options={DOMAIN_OWNERSHIP}
          label={`Ownership of ${host}`}
          disabled={!canAdmin}
        />
      </div>
      <div className="col-start-3 row-start-1 flex justify-end @min-[40rem]:col-start-5">
        {verbs.length > 0 && <VerbActions dim verbs={verbs} menuLabel={`Actions for ${host}`} />}
      </div>
      {protection && (
        <div className="col-span-full min-w-0 animate-rise pl-11">
          <div className="ml-1 border-l border-hairline pl-4">
            <FieldRow>
              <Field label="User name" htmlFor={`domain-user-${index}`}>
                <Input
                  id={`domain-user-${index}`}
                  value={protection.username}
                  readOnly={!canAdmin}
                  autoComplete="off"
                  className="font-mono"
                  onChange={(event) => setProtection({ username: event.target.value })}
                />
              </Field>
              {/* The rule is what is needed while typing, lit as it is met;
                  that an empty field keeps the saved one is the placeholder's
                  "Unchanged", and the rest is behind ⓘ. */}
              <Field
                label="Password"
                htmlFor={`domain-password-${index}`}
                info="Visitors are asked for this user name and password before the site. Left empty, a saved password stays as it is."
                hint={
                  <span aria-live="polite">
                    <FieldCheck met={(protection.password ?? "").length >= 8}>
                      At least 8 characters
                    </FieldCheck>
                  </span>
                }
              >
                <Input
                  id={`domain-password-${index}`}
                  type="password"
                  autoComplete="new-password"
                  value={protection.password ?? ""}
                  readOnly={!canAdmin}
                  className="font-mono"
                  placeholder={protection.hash ? "Unchanged" : ""}
                  onChange={(event) => setProtection({ password: event.target.value })}
                />
              </Field>
            </FieldRow>
          </div>
        </div>
      )}
      {domain.redirectTo && (
        <div className="col-span-full min-w-0 animate-rise pl-11">
          <div className="ml-1 border-l border-hairline pl-4">
            <FieldRow>
              <Field
                label="Sends visitors to"
                htmlFor={`domain-redirect-${index}`}
                info="Every path and query on this name is answered with a redirect to the same path on the other. It serves no application of its own."
              >
                <Select
                  value={domain.redirectTo}
                  onValueChange={(redirectTo) => onChange({ redirectTo })}
                  disabled={!canAdmin}
                >
                  <SelectTrigger id={`domain-redirect-${index}`} className="w-full font-mono">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {/* The saved target stays listed even once it has stopped
                        serving, so the field shows what the server refused. */}
                    {[...new Set([domain.redirectTo, ...targets])].map((name) => (
                      <SelectItem key={name} value={name} className="font-mono">
                        {name}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
              <Field
                label="Answer"
                htmlFor={`domain-redirect-code-${index}`}
                info="Browsers remember a permanent redirect, so use a temporary one while trying it out."
              >
                <Select
                  value={String(domain.redirectCode ?? 308)}
                  onValueChange={(code) =>
                    onChange({
                      redirectCode:
                        code === "308" ? undefined : (Number(code) as DeploymentRedirectCode),
                    })
                  }
                  disabled={!canAdmin}
                >
                  <SelectTrigger id={`domain-redirect-code-${index}`} className="w-full">
                    <SelectValue />
                  </SelectTrigger>
                  <SelectContent>
                    {REDIRECT_CODES.map(([code, label]) => (
                      <SelectItem key={code} value={String(code)}>
                        {label}
                      </SelectItem>
                    ))}
                  </SelectContent>
                </Select>
              </Field>
            </FieldRow>
          </div>
        </div>
      )}
      {error && (
        <p role="alert" className="col-span-full animate-rise pl-11 text-hint text-destructive">
          {error}
        </p>
      )}
    </li>
  )
}

/**
 * The certificate answer for a typed name, as the one word the field carries
 * at its label's edge: ready, issued on the deploy, or needing a decision.
 */
function certificateReading(suggestion: DeploymentHostnameSuggestion): {
  tone: DotTone
  label: string
} {
  if (suggestion.covered) return { tone: "running", label: "HTTPS ready" }
  if (suggestion.certificateMethod) return { tone: "notice", label: "Certificate on deploy" }
  return { tone: "warning", label: "HTTPS unavailable" }
}

/** The tunnel's answer for a typed name: routed through it on the deploy, or not. */
function tunnelReading(suggestion: DeploymentHostnameSuggestion): { tone: DotTone; label: string } {
  return suggestion.covered
    ? { tone: "running", label: "Through the tunnel" }
    : { tone: "warning", label: "Tunnel unavailable" }
}

/** The challenge a certificate would be issued over, as the product that answers it. */
const METHOD_PRODUCT: Partial<
  Record<NonNullable<DeploymentHostnameSuggestion["certificateMethod"]>, string>
> = { caddy: "caddy", nginx: "nginx-static" }

/**
 * The one place a hostname is typed and checked before it is committed — the
 * same certificate-coverage read the new-project flow does, ported here so an
 * existing environment gets the same answer before adding a second name.
 *
 * The name and whether it is served over HTTPS are one field (§7's
 * `InputGroup`), as they are on `/deploy/new`'s public address; the check is
 * the button at its end rather than a "Checking…" line under it, and what the
 * check found is a word at the label's edge, with how a certificate will be
 * ordered as the field's own line under it. Only the case that asks for a
 * decision — automatic HTTPS that cannot be confirmed — is a notice; that a
 * certificate is ready, or will be issued, is a fact nobody has to act on
 * (§14). Where the server says which address the name must point at, the
 * record to create is drawn as one line to copy, with whether it already
 * does.
 *
 * That the record has to point here before the check means anything is
 * behind the field's ⓘ: it is read once, and as a hint it sat under the field
 * until the check and then vanished, pulling everything under it up a line.
 */
function AddDomainPanel({
  open,
  onOpenChange,
  unsaved,
  onAdd,
}: {
  open: boolean
  onOpenChange: (open: boolean) => void
  /**
   * The list's own unsaved edits. Adding writes the whole list as it stands,
   * so a Remove or an HTTPS toggle still waiting for Save goes with it — and
   * the sheet has to say so rather than commit it silently.
   */
  unsaved: number
  onAdd: (domain: DomainValue) => Promise<void>
}) {
  const [hostname, setHostname] = useSessionState("deploy.settings.domains.add.hostname", "")
  const [https, setHttps] = useSessionState("deploy.settings.domains.add.https", true)
  const [tunnel, setTunnel] = useSessionState("deploy.settings.domains.add.tunnel", false)
  const [guarded, setGuarded] = useState(false)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [checking, setChecking] = useState(false)
  const [saving, setSaving] = useState(false)
  const [suggestion, setSuggestion] = useState<DeploymentHostnameSuggestion>()

  const reset = () => {
    setHostname("")
    setHttps(true)
    setTunnel(false)
    setGuarded(false)
    setUsername("")
    setPassword("")
    setSuggestion(undefined)
  }

  // The newest question asked. Leaving the field asks one and the tunnel
  // switch, pressed straight after, asks another; only the newest answer is
  // the sheet's, whichever arrives last.
  const asked = useRef(0)
  const check = async (through = tunnel) => {
    const value = hostname.trim().toLowerCase()
    if (!value) return
    const question = ++asked.current
    setChecking(true)
    try {
      const answer = await get<DeploymentHostnameSuggestion>("/deploy/hostname", {
        hostname: value,
        ...(through ? { tunnel: "1" } : {}),
      })
      if (question === asked.current) setSuggestion(answer)
    } catch {
      // The saved answer stays on screen; the deploy itself is what settles this.
    } finally {
      if (question === asked.current) setChecking(false)
    }
  }

  const typed = hostname.trim().toLowerCase()
  // An answer is this sheet's only while it was asked the question the sheet
  // now asks: a tunnel's answer says nothing about a certificate here, and
  // the certificate's says nothing about a tunnel.
  const matches =
    suggestion?.hostname.toLowerCase() === typed && (suggestion.method === "tunnel") === tunnel
  const answer =
    matches && suggestion
      ? tunnel
        ? tunnelReading(suggestion)
        : https
          ? certificateReading(suggestion)
          : undefined
      : undefined
  const method = matches && !tunnel ? suggestion?.certificateMethod : undefined
  const glyph = method ? METHOD_PRODUCT[method] : undefined
  const ordered = https && !tunnel && suggestion && !suggestion.covered ? method : undefined
  const record =
    matches && !tunnel && suggestion?.address ? `A  ${typed}  →  ${suggestion.address}` : ""
  const incomplete = guarded && (!username.trim() || password.length < 8)

  const add = async () => {
    if (!typed) return
    setSaving(true)
    try {
      await onAdd({
        hostname: typed,
        https: https || tunnel,
        ownership: "managed",
        ...(tunnel ? { tunnel: true } : {}),
        ...(guarded ? { protection: { username: username.trim(), password } } : {}),
      })
      reset()
    } catch (error) {
      notify.error("Could not save domains", error)
    } finally {
      setSaving(false)
    }
  }

  const close = () => {
    reset()
    onOpenChange(false)
  }

  return (
    <SidePanel
      open={open}
      onOpenChange={(next) => (next ? onOpenChange(true) : close())}
      title="Add domain"
      description="Point a record at this server and check it before adding it."
      width="md"
      footer={
        <>
          {unsaved > 0 && (
            <FormNote className="mr-auto max-sm:basis-full">
              Also saves {plural(unsaved, "unsaved change")}
            </FormNote>
          )}
          <Button variant="outline" onClick={close} disabled={saving}>
            Cancel
          </Button>
          <Button onClick={() => void add()} disabled={!typed || incomplete} pending={saving}>
            Add domain
          </Button>
        </>
      }
    >
      <div className="space-y-5">
        <Field
          label="Hostname"
          htmlFor="add-domain-hostname"
          info={
            tunnel
              ? "The name's zone must be on Cloudflare. Its route and CNAME to the tunnel are set as the release goes live, with the Cloudflare credentials saved under Certificates."
              : "Point a DNS record for the name at this server first. Check reads where it points and whether a certificate can be issued for it."
          }
          // Once checked, what the check found is the Status at the label's
          // edge, the method under the field and the record under that — the
          // server's sentence would be all three again.
          hint={
            ordered && (
              <span key={ordered} className="inline-flex animate-rise items-center gap-1.5">
                {glyph && <ProductGlyph id={glyph} className="size-3" />}
                <span>
                  Ordered over the{" "}
                  {ordered === "caddy" ? "managed Caddy ingress" : `${ordered} challenge`}
                </span>
              </span>
            )
          }
          trailing={
            answer && (
              <Status
                key={answer.label}
                tone={answer.tone}
                label={answer.label}
                className="animate-rise"
              />
            )
          }
        >
          <InputGroup>
            <InputGroupInput
              id="add-domain-hostname"
              value={hostname}
              onChange={(event) => setHostname(event.target.value)}
              onBlur={() => void check()}
              placeholder="app.example.com"
              autoComplete="off"
              spellCheck={false}
              className="font-mono"
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              {!tunnel && <HttpsToggle checked={https} onChange={setHttps} />}
              <InputGroupButton
                aria-label="Check this hostname"
                pending={checking}
                disabled={!typed}
                onClick={() => void check()}
              >
                <RefreshClockwise className="size-3.5" />
                Check
              </InputGroupButton>
            </InputGroupAddon>
          </InputGroup>
        </Field>

        {record && (
          // Keyed on the record, so a second check that names another
          // address rises in as the new answer it is.
          <div key={record} className="min-w-0 animate-rise space-y-1.5">
            <div className="flex min-w-0 items-center justify-between gap-3">
              <p className="text-body font-medium">DNS record</p>
              {suggestion?.resolves !== undefined && (
                <Status
                  key={String(suggestion.resolves)}
                  tone={suggestion.resolves ? "running" : "warning"}
                  label={suggestion.resolves ? "Points here" : "Not pointing here yet"}
                  className="animate-rise"
                />
              )}
            </div>
            {/* The record is what gets copied into a DNS panel, so it is set
                at the well's own 12px rather than shrunk to a hint's 11. */}
            <div className="flex min-w-0 items-center gap-1">
              <Well className="min-w-0 flex-1 py-2 break-all whitespace-pre-wrap">{record}</Well>
              <IconAction
                label="Copy the record"
                onClick={() => void copyText(`${typed}. A ${suggestion?.address}`, "Record copied")}
              >
                <Copy />
              </IconAction>
            </div>
          </div>
        )}

        {tunnel && matches && suggestion && (
          <p
            key={suggestion.detail}
            className="animate-rise text-hint leading-relaxed text-muted-foreground"
          >
            {suggestion.detail}{" "}
            {!suggestion.covered && !typed.startsWith("*.") && (
              <Link href="/proxy/tunnel" className="text-foreground underline underline-offset-4">
                Open Cloudflare Tunnel
              </Link>
            )}
          </p>
        )}

        {https && !tunnel && matches && suggestion && !suggestion.covered && !method && (
          <Notice
            tone="warning"
            icon={Warning}
            title="Automatic HTTPS unavailable"
            className="animate-rise"
          >
            {suggestion.certificateIssue ?? "Certificate readiness could not be confirmed."}
          </Notice>
        )}

        <OptionList>
          <OptionRow
            title="Serve through Cloudflare Tunnel"
            hint="Cloudflare's edge answers the name and HTTPS, and the connector on this host brings the requests here: no record points at this server and no port is opened."
            checked={tunnel}
            onCheckedChange={(next) => {
              setTunnel(next)
              void check(next)
            }}
          />
          <OptionRow
            title="Ask visitors for a password"
            checked={guarded}
            onCheckedChange={setGuarded}
          >
            <FieldRow>
              <Field label="User name" htmlFor="add-domain-user">
                <Input
                  id="add-domain-user"
                  value={username}
                  autoComplete="off"
                  className="font-mono"
                  onChange={(event) => setUsername(event.target.value)}
                />
              </Field>
              {/* Lit as it is met, because it is also why Add domain is still
                  off: the button waits for a user name and this. */}
              <Field
                label="Password"
                htmlFor="add-domain-password"
                hint={
                  <span aria-live="polite">
                    <FieldCheck met={password.length >= 8}>At least 8 characters</FieldCheck>
                  </span>
                }
              >
                <Input
                  id="add-domain-password"
                  type="password"
                  autoComplete="new-password"
                  value={password}
                  className="font-mono"
                  onChange={(event) => setPassword(event.target.value)}
                />
              </Field>
            </FieldRow>
          </OptionRow>
        </OptionList>
      </div>
    </SidePanel>
  )
}

/** The names that serve the application rather than redirect, in list order. */
function servedNames(domains: DomainValue[]) {
  return domains.filter((domain) => !domain.redirectTo).map((domain) => domain.hostname)
}

type RouteRules = {
  allowFrom: string[]
  responseHeaders: { name: string; value: string }[]
  forwardAuth?: DeploymentForwardAuth
  hsts?: DeploymentRouteHSTS
}

type SignInField = "provider" | "verify" | "signIn"

/** Which of the form's fields a refusal names, and which sign-in field. */
function refusedRule(
  field: string | undefined,
): { rule: keyof RouteRules; part?: SignInField } | undefined {
  if (field?.startsWith("runtime.allowFrom")) return { rule: "allowFrom" }
  if (field?.startsWith("runtime.responseHeaders")) return { rule: "responseHeaders" }
  if (field?.startsWith("runtime.hsts")) return { rule: "hsts" }
  if (field?.startsWith("runtime.forwardAuth")) {
    const part = field.slice("runtime.forwardAuth.".length) as SignInField
    return {
      rule: "forwardAuth",
      part: ["provider", "verify", "signIn"].includes(part) ? part : undefined,
    }
  }
  return undefined
}

/** The sign-in as the plan keeps it, trimmed, or nothing when it is off. */
function savedSignIn(gate: DeploymentForwardAuth | undefined): DeploymentForwardAuth | undefined {
  if (!gate) return undefined
  return {
    provider: gate.provider,
    verify: gate.verify.trim(),
    signIn: gate.signIn.trim(),
    ...(gate.previewsOnly ? { previewsOnly: true } : {}),
  }
}

/**
 * Who reaches the domains above, and what every answer carries: the route's
 * allowed addresses, its sign-in, its HSTS and its response headers. They
 * are the route's rather than a hostname's — the proxy serves every name
 * from one site — so they are one form under the list instead of a control
 * repeated on every row. They are listed in the order a request meets them:
 * the address list refuses first, then the sign-in, then the headers are set
 * on whatever is answered. HSTS is one of those headers, sent on the HTTPS
 * domains alone, so it is offered only while one serves the application —
 * or while a policy is saved and has to stay removable.
 *
 * They live in the plan's runtime, beside the upload limit, and this form
 * writes only those four fields of it, so a Runtime save never undoes them
 * and this save never undoes a Runtime edit.
 */
function RouteRulesForm({
  configuration,
  save,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const project = useProject()
  const saved: RouteRules = {
    allowFrom: configuration.runtime.allowFrom ?? [],
    responseHeaders: configuration.runtime.responseHeaders ?? [],
    forwardAuth: configuration.runtime.forwardAuth,
    hsts: configuration.runtime.hsts,
  }
  const draft = useSettingDraft(`deploy.${project.projectId}.settings.route-rules`, saved)
  const rules = draft.value
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<{
    rule: keyof RouteRules
    part?: SignInField
    message: string
  }>()
  const preview = Boolean(configuration.runtime.previewIsolation)

  const onSave = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await save({
        runtime: (latest) => ({
          ...latest.runtime,
          allowFrom: rules.allowFrom.length > 0 ? rules.allowFrom : undefined,
          responseHeaders: rules.responseHeaders.length > 0 ? rules.responseHeaders : undefined,
          forwardAuth: savedSignIn(rules.forwardAuth),
          hsts: savedHSTS(rules.hsts),
        }),
      })
      return true
    } catch (error) {
      const refused = error instanceof ApiError ? refusedRule(error.field) : undefined
      if (error instanceof ApiError && refused) {
        setRefusal({ ...refused, message: error.message })
      } else {
        notify.error("Could not save access and headers", error)
      }
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Access and headers"
      onSave={onSave}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canAdmin}
      onDiscard={() => {
        setRefusal(undefined)
        draft.discard()
      }}
    >
      <SettingSection
        title="Access and headers"
        state={routeAccessState(rules.allowFrom, rules.forwardAuth, preview)}
        status={settingStatus({ dirty: draft.dirty, refused: Boolean(refusal) })}
      >
        <ListField
          id="route-allow-from"
          label="Only these addresses"
          placeholder="203.0.113.7 or 10.0.0.0/8"
          values={rules.allowFrom}
          onChange={(allowFrom) => draft.set({ ...rules, allowFrom })}
          hint="Anyone else is refused with 403, before the password or the sign-in is asked for. Empty lets everyone in."
          error={refusal?.rule === "allowFrom" ? refusal.message : undefined}
          disabled={!canAdmin}
        />
        <RouteSignIn
          gate={rules.forwardAuth}
          onChange={(forwardAuth) => draft.set({ ...rules, forwardAuth })}
          conflicts={signInConflicts(configuration.domains, rules.forwardAuth, preview)}
          refusal={refusal?.rule === "forwardAuth" ? refusal : undefined}
          disabled={!canAdmin}
        />
        {hstsOffered(configuration.domains, rules.hsts) && (
          <RouteHSTS
            hsts={rules.hsts}
            domains={configuration.domains}
            onChange={(hsts) => draft.set({ ...rules, hsts })}
            refusal={refusal?.rule === "hsts" ? refusal.message : undefined}
            disabled={!canAdmin}
          />
        )}
        <HeaderListField
          id="route-response-headers"
          label="Response headers"
          values={rules.responseHeaders}
          onChange={(responseHeaders) => draft.set({ ...rules, responseHeaders })}
          hint="Set on every answer, replacing a header of the same name the application sends."
          error={refusal?.rule === "responseHeaders" ? refusal.message : undefined}
          disabled={!canAdmin}
        />
      </SettingSection>
    </SettingForm>
  )
}

/**
 * HSTS: Strict-Transport-Security on every HTTPS answer the route gives,
 * with the same lifetimes, subdomains and preload a Proxy site's control
 * offers, because it is the same header. A domain served over plain HTTP is
 * never sent it, and the row says which domains are. Preload is behind a
 * confirmation, as it is on the Proxy page: once a browser ships with the
 * name built in, removing the header changes nothing for months.
 */
function RouteHSTS({
  hsts,
  domains,
  onChange,
  refusal,
  disabled,
}: {
  hsts: DeploymentRouteHSTS | undefined
  domains: DeploymentEnvironmentConfiguration["domains"]
  onChange: (hsts: DeploymentRouteHSTS | undefined) => void
  refusal?: string
  disabled: boolean
}) {
  const { confirm, dialog } = useConfirm()
  const https = domains.filter((domain) => domain.https && !domain.redirectTo)
  const plain = domains.filter((domain) => !domain.https && !domain.redirectTo)
  const maxAge = hsts?.maxAge || HSTS_DEFAULT_MAX_AGE
  const listed = HSTS_MAX_AGES.some((age) => age.value === maxAge)
  const update = (patch: Partial<DeploymentRouteHSTS>) => hsts && onChange({ ...hsts, ...patch })
  const askPreload = () =>
    hsts &&
    confirm({
      title: "Ask browsers to preload HSTS",
      description: (
        <>
          The header will carry <code className="font-mono">preload</code>, which lets{" "}
          {https[0]?.hostname ?? "the domain"} be submitted to the list browsers ship with. Once it
          is in, every subdomain must serve HTTPS, and leaving the list takes months of browser
          releases. The policy is raised to at least a year and covers subdomains, as the list
          requires.
        </>
      ),
      confirmLabel: "Turn on preload",
      action: async () => onChange(withPreload(hsts)),
    })
  const sentOn =
    https.length > 0
      ? `Sent on ${https.map((domain) => domain.hostname).join(", ")}`
      : "Sent on no domain until one serves the application over HTTPS"
  const notOn =
    plain.length > 0
      ? `; ${plain.map((domain) => domain.hostname).join(", ")} ${plain.length === 1 ? "is" : "are"} served over plain HTTP and not sent it.`
      : "."
  return (
    <OptionList>
      <OptionRow
        title="HSTS"
        hint={`Tells browsers never to use plain HTTP for the HTTPS domains above again. Hard to undo — a mistake sticks for as long as the policy says. ${sentOn}${notOn}`}
        checked={Boolean(hsts)}
        disabled={disabled}
        onCheckedChange={(on) => onChange(on ? { includeSubDomains: true } : undefined)}
      >
        {hsts && (
          <div className="space-y-3">
            <Field
              label="Remember for"
              htmlFor="route-hsts-max-age"
              hint="Start short while checking every subdomain works over HTTPS, then raise it."
              trailing={
                <Status
                  key={hstsPolicyState(hsts)}
                  tone="running"
                  label={hstsPolicyState(hsts)}
                  className="animate-rise"
                />
              }
            >
              <Select
                value={String(maxAge)}
                onValueChange={(value) =>
                  update({
                    maxAge: Number(value) === HSTS_DEFAULT_MAX_AGE ? undefined : Number(value),
                  })
                }
                disabled={disabled}
              >
                <SelectTrigger id="route-hsts-max-age" className="w-full">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  {!listed && <SelectItem value={String(maxAge)}>{`${maxAge} seconds`}</SelectItem>}
                  {HSTS_MAX_AGES.map((age) => (
                    <SelectItem
                      key={age.value}
                      value={String(age.value)}
                      disabled={hsts.preload && age.value < HSTS_PRELOAD_MAX_AGE}
                    >
                      {age.label}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </Field>
            <OptionList>
              <OptionRow
                title="Include subdomains"
                hint="Every name under the HTTPS domains must serve HTTPS too, including ones on other servers."
                checked={Boolean(hsts.includeSubDomains)}
                disabled={disabled}
                onCheckedChange={(on) =>
                  update({
                    includeSubDomains: on || undefined,
                    preload: on ? hsts.preload : undefined,
                  })
                }
              />
              <OptionRow
                title="Preload"
                hint="Lets the domain be built into browsers. Needs a year and subdomains; very slow to undo."
                checked={Boolean(hsts.preload)}
                disabled={disabled}
                onCheckedChange={(on) => (on ? askPreload() : update({ preload: undefined }))}
              />
            </OptionList>
            {refusal && (
              <FormNote role="alert" tone="danger">
                {refusal}
              </FormNote>
            )}
          </div>
        )}
      </OptionRow>
      {dialog}
    </OptionList>
  )
}

/**
 * Require sign-in: the route asks an auth server about every request, the
 * way a Proxy site's single sign-on does, with the same four servers and
 * the same two addresses. What a deployment adds is who it is for — this
 * environment and its previews, or its previews alone — since a preview's
 * plan is a copy of this one.
 */
function RouteSignIn({
  gate,
  onChange,
  conflicts,
  refusal,
  disabled,
}: {
  gate: DeploymentForwardAuth | undefined
  onChange: (gate: DeploymentForwardAuth | undefined) => void
  conflicts: string[]
  refusal?: { part?: SignInField; message: string }
  disabled: boolean
}) {
  const preset = ssoPreset(gate?.provider)
  const update = (patch: Partial<DeploymentForwardAuth>) => gate && onChange({ ...gate, ...patch })
  return (
    <OptionList>
      <OptionRow
        title="Require sign-in"
        hint="Every request is checked with Authelia, Authentik, oauth2-proxy or another server first. A visitor without a session is sent to sign in and brought back; if the server refuses or does not answer, so does the site."
        checked={Boolean(gate)}
        disabled={disabled}
        onCheckedChange={(on) =>
          onChange(
            on ? { provider: "authelia", verify: SSO_PROVIDERS[0].verify, signIn: "" } : undefined,
          )
        }
      >
        {gate && (
          <div className="flex flex-col gap-3">
            <ToggleGroup
              type="single"
              aria-label="Auth server"
              value={gate.provider}
              onValueChange={(value) =>
                value && update(chooseSsoProvider(gate, value as ForwardAuthProvider))
              }
              variant="outline"
              size="sm"
              className="w-full flex-wrap"
              disabled={disabled}
            >
              {SSO_PROVIDERS.map((provider) => (
                <ToggleGroupItem
                  key={provider.value}
                  value={provider.value}
                  className="flex-1 text-hint"
                >
                  {provider.label}
                </ToggleGroupItem>
              ))}
            </ToggleGroup>
            <Field
              label="Check address"
              htmlFor="route-sso-verify"
              hint="Where the proxy asks about each request, with the visitor's headers and the address they asked for. On Caddy, an address on 127.0.0.1 is reached through the container that publishes it."
              error={refusal?.part === "verify" ? refusal.message : undefined}
            >
              <Input
                id="route-sso-verify"
                value={gate.verify}
                onChange={(event) => update({ verify: event.target.value.trim() })}
                placeholder={preset.verify || "http://127.0.0.1:8080/verify"}
                className="font-mono text-hint"
                disabled={disabled}
              />
            </Field>
            <Field
              label="Sign-in page"
              htmlFor="route-sso-signin"
              hint="A visitor without a session is sent here, with rd= set to the address they asked for."
              error={refusal?.part === "signIn" ? refusal.message : undefined}
            >
              <Input
                id="route-sso-signin"
                value={gate.signIn}
                onChange={(event) => update({ signIn: event.target.value.trim() })}
                placeholder={preset.signIn}
                className="font-mono text-hint"
                disabled={disabled}
              />
            </Field>
            <ToggleGroup
              type="single"
              aria-label="Who signs in"
              value={gate.previewsOnly ? "previews" : "everywhere"}
              onValueChange={(value) =>
                value && update({ previewsOnly: value === "previews" || undefined })
              }
              variant="outline"
              size="sm"
              className="w-full"
              disabled={disabled}
            >
              <ToggleGroupItem value="everywhere" className="flex-1 text-hint">
                Here and on previews
              </ToggleGroupItem>
              <ToggleGroupItem value="previews" className="flex-1 text-hint">
                Previews only
              </ToggleGroupItem>
            </ToggleGroup>
            <FormNote>
              The application is sent Remote-User and Remote-Email, taken from the server&apos;s{" "}
              {preset.sends}, and never the visitor&apos;s own. Names that redirect answer before
              anyone is asked to sign in.
            </FormNote>
            {conflicts.map((conflict) => (
              <FormNote key={conflict} tone="warning">
                {conflict}
              </FormNote>
            ))}
            {refusal && (!refusal.part || refusal.part === "provider") && (
              <FormNote role="alert" tone="danger">
                {refusal.message}
              </FormNote>
            )}
          </div>
        )}
      </OptionRow>
    </OptionList>
  )
}

/**
 * Maintenance — the switch. It is not part of the plan: every route a release
 * writes carries the check, and a file beside the route turns it on, which
 * both proxies look for on every request. So this save applies at once,
 * without a deployment and without the route being written again, and
 * neither a rollback nor the next release turns it on or off. Who is let
 * past and what the 503 says are the route's, in the form under it.
 */
function MaintenanceForm({
  projectId,
  environmentId,
}: {
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  // It takes the application away from its visitors, as stopping it does,
  // and the server asks the same capability.
  const canSwitch = can("destructive")
  const path = `/deploy/${projectId}/environments/${environmentId}/maintenance`
  const live = usePoll(
    (signal) => get<DeploymentMaintenanceSwitch>(path, undefined, signal),
    15_000,
    [path],
  )
  const switched = live.data
  const draft = useSettingDraft(`deploy.${projectId}.settings.maintenance`, {
    on: switched?.on ?? false,
  })
  const [saving, setSaving] = useState(false)
  const [refusal, setRefusal] = useState<string>()
  const unready = maintenanceUnready(switched)

  const onSave = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await put<DeploymentMaintenanceSwitch>(path, { on: draft.value.on })
      live.refresh()
      return true
    } catch (error) {
      setRefusal(error instanceof Error ? error.message : String(error))
      return false
    } finally {
      setSaving(false)
    }
  }

  return (
    <SettingForm
      name="Maintenance"
      onSave={onSave}
      dirty={draft.dirty}
      changes={draft.changes}
      saving={saving}
      canEdit={canSwitch}
      applies="immediately"
      onDiscard={() => {
        setRefusal(undefined)
        draft.discard()
      }}
    >
      <SettingSection
        id="maintenance"
        title="Maintenance"
        state={
          <span>
            {maintenanceState(switched)}
            {switched?.on && switched.since && (
              <>
                {" · turned on "}
                <time dateTime={switched.since} title={timestamp(switched.since)}>
                  {relativeTime(switched.since)}
                </time>
              </>
            )}
          </span>
        }
        status={
          draft.dirty || refusal ? (
            settingStatus({ dirty: draft.dirty, refused: Boolean(refusal) })
          ) : switched?.on ? (
            <Status key="on" tone="warning" label="In maintenance" className="animate-rise" />
          ) : null
        }
      >
        <OptionList>
          <OptionRow
            title="Answer with the maintenance page"
            hint="Every visitor gets the maintenance page with a 503 as soon as this is saved, without a deployment, except the addresses let past below. Certificate renewals keep working."
            tone={draft.value.on ? "warning" : "default"}
            checked={draft.value.on}
            disabled={!canSwitch || (!draft.value.on && !switched?.ready)}
            onCheckedChange={(on) => draft.set({ on })}
          />
        </OptionList>
        {unready && <FormNote>{unready}</FormNote>}
        {live.error && !switched && (
          <FormNote tone="warning">The live route could not be read: {live.error.message}</FormNote>
        )}
        {refusal && (
          <FormNote role="alert" tone="danger">
            {refusal}
          </FormNote>
        )}
      </SettingSection>
    </SettingForm>
  )
}

type RoutePages = {
  retryAfter: string
  bypassFrom: string[]
  errorPages: DeploymentErrorPage[]
  interceptErrors: boolean
}

/** What a page's code is for, under its switch. */
const ERROR_PAGE_HINT: Record<DeploymentErrorPage, string> = {
  404: "A path with nothing behind it.",
  502: "The application is down or starting. The page shipped for it retries every 5 seconds.",
  503: "The application says it cannot take requests.",
  504: "The application took longer than the proxy waits to answer.",
}

/** Which of the form's fields a refusal names. */
function refusedPage(field: string | undefined): keyof RoutePages | undefined {
  if (field?.startsWith("runtime.maintenance.retryAfter")) return "retryAfter"
  if (field?.startsWith("runtime.maintenance.bypassFrom")) return "bypassFrom"
  if (field?.startsWith("runtime.errorPages")) return "errorPages"
  if (field?.startsWith("runtime.interceptErrors")) return "interceptErrors"
  return undefined
}

/**
 * The maintenance page and the error pages: who is let past while the switch
 * above is on, what its 503 tells clients, and which answers get a page of
 * the route's own rather than the proxy's. They are written into the route,
 * so they apply with the next deployment, like the rest of the plan. The
 * pages themselves are files beside the route that either proxy reads on
 * every answer, so editing one applies at once.
 */
function RoutePagesForm({
  configuration,
  save,
  projectId,
  environmentId,
}: {
  configuration: DeploymentEnvironmentConfiguration
  save: ReturnType<typeof useConfiguration>["save"]
  projectId: number
  environmentId: number
}) {
  const { can } = useAuth()
  const canAdmin = can("system.admin")
  const { runtime } = configuration
  const saved: RoutePages = {
    retryAfter: runtime.maintenance?.retryAfter ? String(runtime.maintenance.retryAfter) : "",
    bypassFrom: runtime.maintenance?.bypassFrom ?? [],
    errorPages: runtime.errorPages ?? [],
    interceptErrors: runtime.interceptErrors ?? false,
  }
  const draft = useSettingDraft(`deploy.${projectId}.settings.route-pages`, saved)
  const pages = draft.value
  const [saving, setSaving] = useState(false)
  const [finding, setFinding] = useState(false)
  const [refusal, setRefusal] = useState<{ field: keyof RoutePages; message: string }>()
  const [editing, setEditing] = useState<DeploymentPageName | null>(null)
  const retryAfter = retryAfterOf(pages.retryAfter)
  const signsIn = Boolean(runtime.forwardAuth)
  const set = (patch: Partial<RoutePages>) => draft.set({ ...pages, ...patch })

  const onSave = async () => {
    setSaving(true)
    setRefusal(undefined)
    try {
      await save({
        runtime: (latest) => ({
          ...latest.runtime,
          maintenance: routeMaintenanceOf(retryAfter ?? undefined, pages.bypassFrom),
          errorPages: pages.errorPages.length > 0 ? pages.errorPages : undefined,
          interceptErrors: pages.errorPages.length > 0 && pages.interceptErrors ? true : undefined,
        }),
      })
      return true
    } catch (error) {
      const field = error instanceof ApiError ? refusedPage(error.field) : undefined
      if (error instanceof ApiError && field) setRefusal({ field, message: error.message })
      else notify.error("Could not save the pages", error)
      return false
    } finally {
      setSaving(false)
    }
  }

  const addMyAddress = async () => {
    setFinding(true)
    try {
      const { client } = await get<Exposure>("/exposure")
      if (!client) {
        notify.error("The dashboard could not tell which address you come from")
        return
      }
      if (!pages.bypassFrom.includes(client)) set({ bypassFrom: [...pages.bypassFrom, client] })
    } catch (error) {
      notify.error("Could not read your address", error)
    } finally {
      setFinding(false)
    }
  }

  const editable: DeploymentPageName[] = [
    "maintenance",
    ...ERROR_PAGES.filter((code) => saved.errorPages.includes(code)).map(
      (code) => String(code) as DeploymentPageName,
    ),
  ]

  return (
    <>
      <SettingForm
        name="Maintenance page and error pages"
        onSave={onSave}
        dirty={draft.dirty}
        changes={draft.changes}
        saving={saving}
        invalid={retryAfter === null}
        canEdit={canAdmin}
        onDiscard={() => {
          setRefusal(undefined)
          draft.discard()
        }}
      >
        <SettingSection
          title="Maintenance page"
          state={maintenancePolicyState(runtime.maintenance)}
          status={settingStatus({
            dirty: draft.changed(["retryAfter", "bypassFrom"]),
            refused: refusal?.field === "retryAfter" || refusal?.field === "bypassFrom",
          })}
        >
          <ListField
            id="route-maintenance-bypass"
            label="Let these addresses past"
            placeholder="203.0.113.7 or 10.0.0.0/8"
            values={pages.bypassFrom}
            onChange={(bypassFrom) => set({ bypassFrom })}
            hint="They reach the application as usual while maintenance is on, so you can check it before visitors come back."
            error={refusal?.field === "bypassFrom" ? refusal.message : undefined}
            disabled={!canAdmin}
          />
          {canAdmin && (
            <div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={addMyAddress}
                pending={finding}
              >
                Add my address
              </Button>
              <FormNote className="mt-1.5">
                The address this browser reaches the dashboard from, which is the one the deployment
                sees only when both are reached the same way.
              </FormNote>
            </div>
          )}
          <Field
            label="Retry after"
            htmlFor="route-maintenance-retry"
            hint="Seconds the 503 tells clients and crawlers to wait. Empty or 0 sends no Retry-After."
            error={
              retryAfter === null
                ? "Whole seconds, at most 86400 (a day)."
                : refusal?.field === "retryAfter"
                  ? refusal.message
                  : undefined
            }
          >
            <Input
              id="route-maintenance-retry"
              value={pages.retryAfter}
              inputMode="numeric"
              placeholder="0"
              onChange={(event) => set({ retryAfter: event.target.value })}
              className="w-32 font-mono text-xs"
              disabled={!canAdmin}
            />
          </Field>
        </SettingSection>
        <SettingSection
          title="Error pages"
          state={errorPagesState(runtime.errorPages ?? [], runtime.interceptErrors ?? false)}
          status={settingStatus({
            dirty: draft.changed(["errorPages", "interceptErrors"]),
            refused: refusal?.field === "errorPages" || refusal?.field === "interceptErrors",
          })}
        >
          <OptionList>
            {ERROR_PAGES.map((code) => (
              <OptionRow
                key={code}
                title={`Own ${PAGE_LABEL[String(code) as DeploymentPageName]} page`}
                hint={ERROR_PAGE_HINT[code]}
                checked={pages.errorPages.includes(code)}
                disabled={!canAdmin}
                onCheckedChange={(on) =>
                  set({
                    errorPages: ERROR_PAGES.filter((c) =>
                      c === code ? on : pages.errorPages.includes(c),
                    ),
                  })
                }
              />
            ))}
            {pages.errorPages.length > 0 && (
              <OptionRow
                title="Replace the application's own error pages"
                hint={
                  signsIn
                    ? "Not with sign-in on: the application's own 401s would be replaced as well."
                    : "Without it only the errors the proxy produces itself get these pages; with it, the application's answers with those codes do too."
                }
                checked={pages.interceptErrors}
                disabled={!canAdmin || (signsIn && !pages.interceptErrors)}
                onCheckedChange={(interceptErrors) => set({ interceptErrors })}
              />
            )}
          </OptionList>
          {refusal && (refusal.field === "errorPages" || refusal.field === "interceptErrors") && (
            <FormNote role="alert" tone="danger">
              {refusal.message}
            </FormNote>
          )}
          {canAdmin && (
            <div className="flex flex-wrap gap-2">
              {editable.map((name) => (
                <Button
                  key={name}
                  type="button"
                  size="sm"
                  variant="outline"
                  onClick={() => setEditing(name)}
                >
                  Edit {PAGE_LABEL[name]}
                </Button>
              ))}
            </div>
          )}
          <FormNote>
            Saved pages are served from the next answer that uses them. Which pages the route
            answers with, and who is let past, change with the next deployment.
          </FormNote>
        </SettingSection>
      </SettingForm>
      {canAdmin && (
        <PageEditor
          key={editing ?? "none"}
          open={editing !== null}
          onOpenChange={(open) => !open && setEditing(null)}
          site={
            configuration.domains.find((domain) => !domain.redirectTo)?.hostname ??
            "this deployment"
          }
          page={editing ?? "maintenance"}
          path={`/deploy/${projectId}/environments/${environmentId}/pages/${editing ?? "maintenance"}`}
          server="the proxy"
        />
      )}
    </>
  )
}
