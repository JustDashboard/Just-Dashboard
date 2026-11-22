"use client"

import { useEffect, useRef, useState } from "react"
import Link from "next/link"
import { useRouter, useSearchParams } from "next/navigation"
import { useMemoryState, useSessionState } from "@/lib/view-state"
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
  Servers,
  Trash,
  Warning,
} from "@/components/icons"
import { ApiError, get, put, refusedIndex } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural, relativeTime, timestamp } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import { useAppDomain } from "@/hooks/use-app-domain"
import { usePoll } from "@/hooks/use-poll"
import { appDomainStateOf, zoneWords } from "@/lib/app-domain"
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
} from "@/lib/types"
import { SSO_PROVIDERS, chooseSsoProvider, ssoPreset } from "@/lib/sso-providers"
import {
  Field,
  FieldCheck,
  FieldChecks,
  FieldRow,
  FormNote,
  OptionList,
  OptionRow,
} from "@/components/form"
import { Well } from "@/components/panel"
import { ProductGlyph, ProductLogo, issuerProduct } from "@/components/product-logo"
import { SidePanel } from "@/components/side-panel"
import { EmptyState, Notice } from "@/components/state"
import { Status, type DotTone } from "@/components/status-dot"
import { VerbActions, type Verb } from "@/components/verbs"
import { IconAction } from "@/components/icon-action"
import { Button } from "@/components/ui/button"
import { Explain } from "@/components/explain"
import { HoverTip } from "@/components/deploy/settings/hover-tip"
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
import { Segments } from "@/components/segments"
import { HttpsToggle } from "@/components/deploy/https-toggle"
import {
  CERTIFICATE_LABEL,
  CertificateStatus,
  ROUTE_LABEL,
  ROUTE_TONE,
  hostOf,
} from "@/components/deploy/vocabulary"
import { INLINE_LINK } from "@/components/inline-link"
import { SettingForm, SettingSection, settingStatus } from "@/components/settings/setting-card"
import { SettingsPage } from "@/components/settings/settings-page"
import {
  draftChanges,
  useConfiguration,
  useSettingDraft,
} from "@/components/deploy/settings/use-configuration"
import {
  withPasswords,
  withTypedPassword,
  withoutPassword,
} from "@/components/deploy/settings/domain-passwords"
import {
  PASSWORD_MIN,
  hostnameFromInput,
  hostnameProblem,
  httpsTip,
  isWildcard,
  passwordProblem,
  passwordTip,
  protectionUserProblem,
  protectionsIncomplete,
  redirectTargets,
  sharedUser,
  tunnelTip,
  withRedirect,
  withoutDomain,
} from "@/components/deploy/settings/domain-rows"
import { OwnershipSelect } from "@/components/deploy/settings/mounts"
import { useDraftScope, useProject } from "@/components/deploy/project-context"
import { EdgeRulesForm } from "@/components/deploy/settings/edge-rules"
import { DNSRecordPanel } from "@/components/deploy/settings/dns-record-panel"
import {
  HSTS_DEFAULT_MAX_AGE,
  HSTS_MAX_AGES,
  HSTS_PRELOAD_MAX_AGE,
  HeaderListField,
} from "@/components/proxy/site-form"
import { ListField } from "@/components/list-field"
import { Popover, PopoverContent, PopoverTrigger } from "@/components/ui/popover"
import { PAGE_LABEL, PageEditor } from "@/components/proxy/page-editor"
import { siteHref } from "@/components/proxy/site-tabs"
import { useConfirm } from "@/components/confirm-dialog"
import {
  gatesHere,
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
 * footnote under the card. Now each hostname is one row of one height:
 * the certificate's issuer drawn as itself (Let's Encrypt, from the issuer the
 * live release's certificate names), the name as a link to the site with one
 * line of readings under it, then the switches it carries as one segmented
 * control, who owns it and its menu, all on one axis. The line under the name
 * holds what isn't the expected answer; what each switch means, who renews the
 * certificate and what ownership decides are tooltips on the thing itself. The
 * site and the certificate behind it are verbs in the row's menu rather than
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
            Boolean(configuration.runtime.edgeRules?.length) ||
            Boolean(configuration.runtime.rateLimit) ||
            Boolean(configuration.runtime.crowdSec)) && (
            <EdgeRulesForm configuration={configuration} save={state.save} />
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
      invalid={blockers.length > 0}
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
            hint="Only devices on your tailnet reach it: no public domain or certificate"
            checked={value.on}
            disabled={!canAdmin}
            onCheckedChange={(on) => draft.set({ ...value, on })}
          >
            {value.on && (
              <div className="flex flex-col gap-4">
                <TailnetAddress live={live} saved={Boolean(savedTailnet)} />
                <ListField
                  id="tailnet-allow"
                  label="Who may open it"
                  placeholder="alice@example.com or tag:ci"
                  values={value.allow}
                  onChange={(allow) => draft.set({ ...value, allow })}
                  parse={readTailnetEntry}
                  hint="Tailscale logins and tags. Empty lets everyone on the tailnet in."
                  info="A login is the account a person signs in to Tailscale with, alice@example.com or alice@github. A tag is a machine's ACL tag, tag:ci; a tagged machine is admitted by its tags alone."
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
  // A password typed on a saved name is a secret before it is saved, and the
  // draft above is written to sessionStorage: it stays in memory, beside the
  // draft, and joins it only on the way to the server.
  const scope = useDraftScope()
  const [passwords, setPasswords] = useMemoryState<Record<string, string>>(
    `deploy.${project.projectId}.settings.domains.passwords${scope}`,
    {},
  )
  const toSave = withPasswords(domains, passwords)
  const changes = draftChanges(toSave, configuration.domains)
  // A tab open across the upgrade can still hold a draft written before this.
  const written = draft.value.some((domain) => domain.protection?.password)
  useEffect(() => {
    if (written) setDomains((current) => current.map(withoutPassword))
  }, [written, setDomains])
  const [saving, setSaving] = useState(false)
  const [adding, setAdding] = useSessionState(
    `deploy.${project.projectId}.settings.domains.adding`,
    false,
  )
  const [rowError, setRowError] = useState<{ index: number; message: string }>()
  // A name just turned into a redirect: its row opens the two choices, where
  // the first target used to be chosen without a word.
  const [redirecting, setRedirecting] = useState<string>()
  // The name whose DNS record sheet is open. A preflight finding's Create the
  // record opens the page on it (`?record=`), and the address is then dropped
  // so a reload does not open it again.
  const search = useSearchParams()
  const asked = canAdmin ? search.get("record") : null
  const [recordFor, setRecordFor] = useState<string | undefined>(asked ?? undefined)
  const [answered, setAnswered] = useState(asked)
  if (asked !== answered) {
    setAnswered(asked)
    if (asked) setRecordFor(asked)
  }
  useEffect(() => {
    if (asked) router.replace(window.location.pathname, { scroll: false })
  }, [asked, router])
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
  // This server's just-dashboard.com address, for the control that adds its name and for
  // marking a name under a handle the server no longer holds.
  const appDomain = useAppDomain({ enabled: canAdmin })
  const appStatus = appDomain.status
  const appName = useAppDomainName(
    project.detail.project.name,
    appStatus?.enabled && appStatus.domain?.status === "active"
      ? appStatus.domain.handle
      : undefined,
  )
  const { bindAddress } = configuration.runtime
  const publicBind = bindAddress === "0.0.0.0" || bindAddress === "::"

  // A refusal is about the row as it was sent, so changing the row answers it.
  const update = (index: number, next: Partial<DomainValue>) => {
    if (rowError?.index === index) setRowError(undefined)
    setDomains(domains.map((item, i) => (i === index ? { ...item, ...next } : item)))
  }

  // What the rest of the route rules out for a row, said on the switch rather
  // than left for the server to refuse the whole list over. Both read what is
  // saved, because the domains are written before the rules under them.
  const gated = gatesHere(
    configuration.runtime.forwardAuth,
    Boolean(configuration.runtime.previewIsolation),
  )
  const fenced = Boolean(configuration.runtime.allowFrom?.length)

  const typePassword = (hostname: string, password: string) =>
    setPasswords((typed) => withTypedPassword(typed, hostname, password))

  const persist = async (next: DomainValue[]) => {
    await save({ domains: next })
    setPasswords({})
    notify.success("Domains saved")
  }

  const onSave = async () => {
    setSaving(true)
    setRowError(undefined)
    try {
      await save({ domains: toSave })
      setPasswords({})
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

  const removeName = (hostname: string) => {
    typePassword(hostname, "")
    setRowError(undefined)
    setDomains(withoutDomain(domains, hostname))
  }

  // Served through Cloudflare Tunnel, a deployment's names live in the operator's own zone.
  const tunnelled = domains.length > 0 && domains.every((domain) => domain.tunnel)
  const appNameHere = appName ? drafted.has(appName) : false
  const [assigning, setAssigning] = useState(false)
  const switchAppName = async (on: boolean) => {
    if (!appName) return
    if (!on) return removeName(appName)
    // A saved name removed only in the draft comes back as it was saved.
    const kept = configuration.domains.find((domain) => domain.hostname.toLowerCase() === appName)
    if (kept) return setDomains([...domains, kept])
    setAssigning(true)
    try {
      await persist([...toSave, { hostname: appName, https: true, ownership: "managed" }])
    } catch (error) {
      notify.error("Could not save domains", error)
    } finally {
      setAssigning(false)
    }
  }

  const verbsFor = (domain: DomainValue, index: number): Verb[] => {
    const route = routeFor(domain.hostname)
    const verbs: Verb[] = []
    if (route?.deepLink)
      verbs.push({
        key: "site",
        label: "Open the Proxy site",
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
    // A saved name's record, read and written with the DNS credentials saved
    // under Certificates; an unsaved one has nothing on the server to ask.
    if (canAdmin && saved.has(domain.hostname.toLowerCase()))
      verbs.push({
        key: "dns-record",
        label: "Open the DNS record",
        icon: Servers,
        run: () => setRecordFor(domain.hostname),
      })
    // A wildcard answers names nobody listed, so it has no address to send
    // on from and the server refuses it.
    if (
      canAdmin &&
      !domain.redirectTo &&
      !isWildcard(domain.hostname) &&
      redirectTargets(domains, domain.hostname).length > 0
    )
      verbs.push({
        key: "redirect",
        label: "Redirect to another domain",
        icon: CornerUpRight,
        run: () => {
          setDomains(withRedirect(domains, domain.hostname))
          setRedirecting(domain.hostname)
        },
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
        run: () => removeName(domain.hostname),
      })
    return verbs
  }

  return (
    <>
      <SettingForm
        name="Domains"
        onSave={onSave}
        dirty={changes > 0}
        changes={changes}
        saving={saving}
        invalid={protectionsIncomplete(domains, passwords)}
        canEdit={canAdmin}
        onDiscard={() => {
          setRowError(undefined)
          setPasswords({})
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
                href={siteHref(observed.siteName)}
                className="flex min-w-0 items-center gap-1 rounded-sm font-mono focus-ring transition-colors hover:text-foreground hover:underline"
              >
                <span className="truncate">{observed.siteName}</span>
                <ArrowUpRight aria-hidden className="size-3 shrink-0" />
              </Link>
            )
          }
          status={settingStatus({ dirty: changes > 0, refused: Boolean(rowError) })}
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
              description="Add a hostname to route its traffic here through Proxy, with its certificate issued on the next deployment."
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
                  targets={redirectTargets(domains, domain.hostname)}
                  verbs={verbsFor(domain, index)}
                  onChange={(next) => update(index, next)}
                  gated={gated}
                  fenced={fenced}
                  savedProtection={
                    configuration.domains.find((item) => item.hostname === domain.hostname)
                      ?.protection
                  }
                  sharedWith={sharedUser(domains, passwords, domain.hostname)}
                  password={passwords[domain.hostname] ?? ""}
                  onPassword={(password) => typePassword(domain.hostname, password)}
                  askRedirect={redirecting === domain.hostname}
                  onRedirectAsked={() => setRedirecting(undefined)}
                  appDomain={appDomainStateOf(
                    domain.hostname,
                    routeFor(domain.hostname),
                    appStatus,
                  )}
                  onRemove={canAdmin ? () => removeName(domain.hostname) : undefined}
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
          {canAdmin && appName && appStatus && !tunnelled && (
            <OptionList>
              <OptionRow
                title={`${zoneWords(appStatus)} address`}
                hint={
                  <>
                    <span className="font-mono text-foreground">{appName}</span>, covered by this
                    server&apos;s wildcard certificate
                  </>
                }
                checked={appNameHere}
                disabled={assigning}
                onCheckedChange={(on) => void switchAppName(on)}
              />
            </OptionList>
          )}
        </SettingSection>
      </SettingForm>

      <DNSRecordPanel
        projectId={project.projectId}
        hostname={recordFor}
        onOpenChange={(open) => !open && setRecordFor(undefined)}
      />

      <AddDomainPanel
        open={adding}
        onOpenChange={setAdding}
        unsaved={changes}
        existing={domains}
        gated={gated}
        fenced={fenced}
        onAdd={async (domain) => {
          await persist([...toSave, domain])
          setAdding(false)
        }}
      />
    </>
  )
}

/**
 * One hostname. Wide, it is one line: the issuer's mark, the name with one line
 * of readings under it, the segmented switches, who owns it and its menu, every
 * control the same height on the same axis. Narrower than that, the controls
 * take a second line of their own and the menu stays pinned to the name — one
 * grid reflowed, so nothing is drawn twice.
 *
 * The readings hold what isn't the expected answer. It used to print what the
 * HTTPS switch beside it already said, and who renews the certificate, and they
 * wrapped into a stack four lines tall beside controls that sat on none of
 * them; those are tooltips on the switch and the certificate now.
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
  gated,
  fenced,
  savedProtection,
  sharedWith,
  password,
  onPassword,
  askRedirect,
  onRedirectAsked,
  appDomain,
  onRemove,
}: {
  /**
   * The row was just made a redirect from its menu: open where it sends
   * visitors and with which answer, both at once, as the press asked.
   */
  askRedirect?: boolean
  onRedirectAsked?: () => void
  /** A name under the site's app-domain zone; `detached` when this server no longer holds its handle. */
  appDomain?: "active" | "detached"
  /** Removes the name from the draft, as the menu's Remove does. */
  onRemove?: () => void
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
  /** The route's sign-in applies here, which rules out a password and plain HTTP. */
  gated: boolean
  /** The route has a saved address list, which rules out a tunnel. */
  fenced: boolean
  /** What this name asks for as saved, so the switch off and on again is not a new password. */
  savedProtection?: DomainValue["protection"]
  /** Other names with this user name and another password, of which the route keeps one. */
  sharedWith: string[]
  /** Typed and not saved. It stays out of `domain`, whose draft is written to the tab's storage. */
  password: string
  onPassword: (password: string) => void
}) {
  const host = domain.hostname
  const protection = domain.protection
  const scheme = domain.https ? "https" : "http"
  const wildcard = isWildcard(host)
  const [routeTone, routeLabel]: [DotTone, string] = unsaved
    ? ["notice", "Not saved yet"]
    : route
      ? [ROUTE_TONE[route.route], ROUTE_LABEL[route.route]]
      : pending
        ? ["notice", "Routes on the next deployment"]
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
  // A plain name's "HTTP only" is what the line already says, and an unread
  // certificate beside an unread route would be the same two words twice.
  const certificateRead =
    liveCertificate &&
    liveCertificate.certificate !== "not requested" &&
    !(liveCertificate.certificate === "unavailable" && liveCertificate.route === "unavailable")
      ? liveCertificate
      : undefined
  const renewal = certificateRead && certificateTip(certificateRead)
  // A certificate that wants attention leads the line's facts, because the
  // line cuts what does not fit: behind a redirect's target, "Certificate
  // expired" was cut down to its dot.
  const certificateUrgent =
    certificateRead !== undefined &&
    (certificateRead.certificate === "expired" ||
      certificateRead.certificate === "expiring" ||
      certificateRead.certificate === "missing")

  const issuer = tunnel ? "cloudflare" : issuerProduct(liveCertificate?.certificateIssuer)

  const setProtection = (next: Partial<NonNullable<DomainValue["protection"]>>) =>
    protection && onChange({ protection: { ...protection, ...next } })

  const nextScheme = domain.https ? "Certificate on deploy" : "HTTP only on deploy"
  // What the rest of the route holds a switch at. They stay pressable targets
  // rather than `disabled`, which a browser sends no pointer events to, so the
  // tooltip can still say why.
  const httpsHeld = tunnel || (gated && domain.https && !domain.redirectTo)
  const passwordHeld = gated && !protection
  const tunnelHeld = !tunnel && (wildcard || fenced || Boolean(appDomain))
  const typedProblem = passwordProblem(password)
  const [explaining, setExplaining] = useState(false)
  // The sign-in's fields open by themselves when Password is turned on, since
  // that press is a request to set them; after that the row's fact opens them.
  const [asking, setAsking] = useState(false)
  // Where a redirecting name sends visitors, and with which answer, opened from its fact.
  const [routing, setRouting] = useState(false)
  // Asked for from the row's menu, the choices are open as soon as the row
  // redirects, until the reader closes them.
  const routeOpen = routing || Boolean(askRedirect && domain.redirectTo)
  const onRouteOpen = (next: boolean) => {
    setRouting(next)
    if (askRedirect) onRedirectAsked?.()
  }
  const signInIncomplete = Boolean(
    protection &&
    (!protection.username ||
      protectionUserProblem(protection.username) ||
      (!protection.hash && !password) ||
      (password && typedProblem)),
  )

  // One group, so turning HTTPS back on brings the live certificate's
  // readings back together rather than popping in one by one. At the line's
  // end it gives way with an ellipsis ("9 da…"), never a second line, and the
  // whole of it is its explanation, opened by hover, focus or a press. The
  // group's own ring is outside the box it cuts, so the cut never clips it.
  const daysLeft =
    certificateRead &&
    typeof certificateRead.certificateDaysLeft === "number" &&
    certificateRead.certificateDaysLeft > 0
      ? certificateRead.certificateDaysLeft
      : undefined
  const certificateWords =
    certificateRead &&
    [
      `${
        certificateRead.certificate === "unavailable"
          ? "Certificate not observed"
          : CERTIFICATE_LABEL[certificateRead.certificate]
      }${daysLeft !== undefined ? `, ${plural(daysLeft, "day")} left` : ""}.`,
      renewal,
    ]
      .filter(Boolean)
      .join(" ")
  const certificateFacts = certificateRead && (
    <Explain face="wrap" tip={certificateWords} label={`Certificate on ${host}`}>
      <span
        // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- takes the keyboard so that what the line cuts can be read without a pointer
        tabIndex={0}
        className={cn(
          "block animate-rise truncate rounded-sm focus-ring",
          // Whole when it leads; at the line's end it gives way.
          certificateUrgent ? "shrink-0" : "min-w-0",
        )}
      >
        <CertificateStatus domain={certificateRead} named />
        {daysLeft !== undefined && (
          <span
            className={cn(
              "numeric ml-3",
              certificateRead.certificate === "expiring" ? "text-warning" : "text-muted-foreground",
            )}
          >
            {plural(daysLeft, "day")} left
          </span>
        )}
      </span>
    </Explain>
  )

  return (
    <li className="grid min-w-0 animate-rise grid-cols-[2rem_minmax(0,1fr)_auto] items-center gap-x-3 gap-y-3 py-3 first:pt-0 @min-[44rem]:grid-cols-[2rem_minmax(0,1fr)_auto_auto_auto]">
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
        {/* A wildcard is every name under it and none in particular, so there
            is no address to open. */}
        {wildcard ? (
          <span className="block truncate font-mono text-sm font-medium" title={host}>
            {host}
          </span>
        ) : (
          <a
            href={`${scheme}://${host}/`}
            target="_blank"
            rel="noreferrer"
            aria-label={`Open ${host}`}
            title={host}
            className="flex max-w-full min-w-0 items-center gap-1.5 rounded-sm font-mono text-sm font-medium focus-ring hover:underline"
          >
            <span className="truncate">{host}</span>
            <External aria-hidden className="size-3 shrink-0 text-muted-foreground" />
          </a>
        )}
        {/* One line, one size, in every state: a fact that does not fit is
            cut at the line's end rather than wrapped onto a second, so the row
            keeps its height with a password on or a redirect set (PGD-26).
            What wants a press comes first, as marks that never clip: the
            sign-in's key, then where a redirecting name sends its visitors.
            A certificate that wants attention stands between the two, since a
            danger at the end of a line that cuts is a danger not seen.
            The days left and who renews it were 11px beside 12px readings,
            two baselines for one row of facts. */}
        <div className="flex h-4.5 min-w-0 items-center gap-x-3 overflow-x-clip text-xs whitespace-nowrap">
          {/* A name under a handle this server no longer holds does not resolve, so it leads
              the line, and what to do about it is a layer over the row, never a band under it. */}
          {appDomain === "detached" && (
            <Popover open={explaining} onOpenChange={setExplaining}>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  aria-label={`${host} is detached`}
                  className="hit-area -m-1 inline-flex shrink-0 animate-rise items-center gap-1 rounded-sm p-1 font-medium text-warning focus-ring hover:bg-row-hover"
                >
                  <Warning aria-hidden className="size-3.5 shrink-0" />
                  Detached
                </button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-80 space-y-3">
                <p className="text-body font-medium">Not this server&apos;s address any more</p>
                <p className="text-xs leading-relaxed whitespace-normal text-muted-foreground">
                  This server no longer holds the just-dashboard.com address {host} is under, so the
                  name does not resolve. The next release leaves it out, or fails if it is the only
                  public name. Claim the address again in Settings › Connect, or remove the name.
                </p>
                <div className="flex flex-wrap items-center gap-2">
                  {onRemove && (
                    <Button
                      size="sm"
                      variant="outline"
                      className="text-destructive"
                      onClick={() => {
                        setExplaining(false)
                        onRemove()
                      }}
                    >
                      <Trash />
                      Remove
                    </Button>
                  )}
                  <Button size="sm" variant="ghost" asChild>
                    <Link href="/dashboard/link#app-domain">Settings › Connect</Link>
                  </Button>
                </div>
              </PopoverContent>
            </Popover>
          )}
          <Status key={routeLabel} tone={routeTone} label={routeLabel} className="animate-rise" />
          {/* The sign-in is a mark on the row and its two fields a layer over
              it, so the list never grows under a row (PGD-26). The mark takes
              the warning ink while the sign-in is incomplete. */}
          {protection && (
            <Popover open={asking} onOpenChange={setAsking}>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  title={`Password · ${protection.username || "no user name"}`}
                  className={cn(
                    // 22px drawn, a finger's 44px pressed (hit-area).
                    "hit-area -m-1 inline-flex shrink-0 animate-rise items-center rounded-sm p-1 focus-ring hover:bg-row-hover",
                    // A user name shared with another password is said in
                    // the popover; the mark carries its ink, or the row
                    // showed nothing of it until the fact was pressed.
                    signInIncomplete || sharedWith.length > 0
                      ? "text-warning"
                      : "text-muted-foreground",
                  )}
                >
                  <Key aria-hidden className="size-3.5 shrink-0" />
                  <span className="sr-only">
                    Password · {protection.username || "no user name"} on {host}
                  </span>
                </button>
              </PopoverTrigger>
              <PopoverContent align="start" className="w-80 space-y-4">
                <Field
                  label="User name"
                  htmlFor={`domain-user-${index}`}
                  error={protectionUserProblem(protection.username)}
                  hint={!protection.username && <FieldCheck met={false}>Required</FieldCheck>}
                >
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
                    "Unchanged", and the rest is behind ⓘ. A password already
                    saved has met it, so nothing is left to light. */}
                <Field
                  label="Password"
                  htmlFor={`domain-password-${index}`}
                  info="Asked before every name this environment serves, whichever one it is set on; the application never sees it. Left empty, a saved password stays as it is."
                  hint={
                    (password || !protection.hash) && (
                      <FieldChecks>
                        <FieldCheck met={password.length > 0 && !typedProblem}>
                          {typedProblem ?? `At least ${PASSWORD_MIN} characters`}
                        </FieldCheck>
                      </FieldChecks>
                    )
                  }
                >
                  <Input
                    id={`domain-password-${index}`}
                    type="password"
                    autoComplete="new-password"
                    value={password}
                    readOnly={!canAdmin}
                    className="font-mono"
                    placeholder={protection.hash ? "Unchanged" : ""}
                    onChange={(event) => onPassword(event.target.value)}
                  />
                </Field>
                {sharedWith.length > 0 && (
                  <FormNote tone="warning">
                    {sharedWith.join(", ")} {sharedWith.length === 1 ? "has" : "have"} this user
                    name with another password, and the route keeps one password per user. Give each
                    its own user name, or the same password.
                  </FormNote>
                )}
              </PopoverContent>
            </Popover>
          )}
          {certificateUrgent && certificateFacts}
          {/* A redirecting name says where it sends visitors, and the two
              choices are a layer over the row: they were a band under it,
              which made the row taller than its siblings. Its target is whole
              unless a certificate that wants attention stands before it; then
              the target gives way, with an ellipsis. */}
          {domain.redirectTo && (
            <Popover open={routeOpen} onOpenChange={onRouteOpen}>
              <PopoverTrigger asChild>
                <button
                  type="button"
                  aria-label={`Sends visitors from ${host} to ${domain.redirectTo}`}
                  title={`Sends visitors to ${domain.redirectTo} · ${domain.redirectCode ?? 308}`}
                  className={cn(
                    "inline-flex max-w-48 min-w-0 animate-rise items-center gap-1 text-muted-foreground",
                    // A fact that opens something is a link on the line, so
                    // it is underlined at rest (INLINE_LINK), not on hover.
                    INLINE_LINK,
                    !certificateUrgent && "shrink-0",
                  )}
                >
                  <CornerUpRight aria-hidden className="size-3 shrink-0" />
                  <span className="truncate font-mono">{domain.redirectTo}</span>
                  <span className="numeric shrink-0">· {domain.redirectCode ?? 308}</span>
                </button>
              </PopoverTrigger>
              <PopoverContent
                align="start"
                className="w-80 space-y-4"
                // The menu that asked for them hands the keyboard back to its
                // trigger once it has gone. That is not the reader leaving
                // the choices: the focus is taken into the first of them.
                onFocusOutside={(event) => {
                  const to = event.detail.originalEvent.target
                  if (!askRedirect || !(to instanceof Element)) return
                  if (!to.closest("[aria-haspopup='menu']")) return
                  event.preventDefault()
                  onRouteOpen(true)
                  document.getElementById(`domain-redirect-${index}`)?.focus()
                }}
              >
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
              </PopoverContent>
            </Popover>
          )}
          {/* Each name is served on its own scheme, so a plain one says so
              whatever the others do. An HTTPS name is the expected answer, and
              what it does for plain visitors is the switch's tooltip. */}
          {tunnel && (
            <span key="tunnel" className="animate-rise text-muted-foreground">
              Through Cloudflare Tunnel
            </span>
          )}
          {!tunnel && !domain.https && !domain.redirectTo && (
            <span key="plain" className="animate-rise text-muted-foreground">
              Plain HTTP, no redirect
            </span>
          )}
          {/* The issuer is the row's mark, so the line carries the state and the
              days left and not the issuer's glyph a second time. */}
          {route && !unsaved && httpsChanging && (
            <Status key={nextScheme} tone="notice" label={nextScheme} className="animate-rise" />
          )}
          {!certificateUrgent && certificateFacts}
        </div>
      </div>
      <div className="col-span-full flex min-w-0 flex-wrap items-center gap-2 pl-11 @min-[44rem]:contents">
        <ToggleGroup
          type="multiple"
          variant="outline"
          value={[domain.https && "https", protection && "password", tunnel && "tunnel"].filter(
            (value): value is string => Boolean(value),
          )}
          onValueChange={(values) => {
            // A held switch stays as it was, whatever the press said.
            const through = tunnelHeld ? false : values.includes("tunnel")
            const guarded = passwordHeld ? false : values.includes("password")
            // Through the tunnel is HTTPS by definition — Cloudflare's edge
            // answers the visitor — so turning it on turns HTTPS on with it.
            const https = through || (httpsHeld && !tunnel) || values.includes("https")
            onChange({
              https,
              protection: guarded ? (protection ?? savedProtection ?? { username: "" }) : undefined,
              tunnel: through || undefined,
            })
            if (guarded && !protection) setAsking(true)
            if (!guarded) {
              onPassword("")
              setAsking(false)
            }
          }}
          disabled={!canAdmin}
          className="shrink-0"
        >
          <HoverTip tip={httpsTip({ https: domain.https, tunnel, gated: httpsHeld && !tunnel })}>
            <Segment
              value="https"
              aria-label={`HTTPS on ${host}`}
              // Held on, not greyed out: the name is HTTPS, at Cloudflare's edge.
              aria-disabled={httpsHeld || undefined}
              className={cn(
                "h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9",
                httpsHeld && "cursor-default hover:bg-transparent",
              )}
            >
              <LockClosed
                className={cn(
                  "size-3.5 transition-colors",
                  domain.https ? "text-brand" : "text-muted-foreground",
                )}
              />
              HTTPS
            </Segment>
          </HoverTip>
          <HoverTip tip={passwordTip({ guarded: Boolean(protection), gated })}>
            <Segment
              value="password"
              aria-label={`Password on ${host}`}
              aria-disabled={passwordHeld || undefined}
              className={cn(
                "h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9",
                passwordHeld && "cursor-not-allowed opacity-50 hover:bg-transparent",
              )}
            >
              <Key
                className={cn(
                  "size-3.5 transition-colors",
                  protection ? "text-brand" : "text-muted-foreground",
                )}
              />
              Password
            </Segment>
          </HoverTip>
          {/* Always drawn, so the rows' segments line up down the list and a
              wildcard's says why it is not offered. */}
          <HoverTip tip={tunnelTip({ tunnel, wildcard, fenced, appDomain: Boolean(appDomain) })}>
            <Segment
              value="tunnel"
              aria-label={`Cloudflare Tunnel on ${host}`}
              aria-disabled={tunnelHeld || undefined}
              className={cn(
                "h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9",
                tunnelHeld && "cursor-not-allowed opacity-50 hover:bg-transparent",
              )}
            >
              <ProductGlyph
                id="cloudflare"
                className={cn("transition-opacity", !tunnel && "opacity-50 grayscale")}
              />
              Tunnel
            </Segment>
          </HoverTip>
        </ToggleGroup>
        <OwnershipSelect
          value={domain.ownership}
          onChange={(ownership) => onChange({ ownership })}
          options={DOMAIN_OWNERSHIP}
          label={`Ownership of ${host}`}
          disabled={!canAdmin}
          quiet
        />
      </div>
      <div className="col-start-3 row-start-1 flex justify-end @min-[44rem]:col-start-5">
        {verbs.length > 0 && <VerbActions dim verbs={verbs} menuLabel={`Actions for ${host}`} />}
      </div>
      {error && (
        <p role="alert" className="col-span-full animate-rise pl-11 text-hint text-destructive">
          {error}
        </p>
      )}
    </li>
  )
}

/**
 * The just-dashboard.com name for this project under the handle this server holds, as the
 * hostname suggestion makes it (`method=app-domain`): the slug cut to one DNS label and kept
 * clear of other projects' names. Asked once per handle, and nothing while none is held.
 */
function useAppDomainName(name: string, handle: string | undefined) {
  const [answer, setAnswer] = useState<{ key: string; hostname: string }>()
  const key = handle ? `${handle}:${name}` : ""
  useEffect(() => {
    if (!key) return
    const controller = new AbortController()
    get<DeploymentHostnameSuggestion>(
      "/deploy/hostname",
      { name, method: "app-domain" },
      controller.signal,
    )
      .then((suggestion) => {
        if (suggestion.method === "app-domain" && suggestion.hostname)
          setAnswer({ key, hostname: suggestion.hostname.toLowerCase() })
      })
      .catch(() => {
        // Without the name the control is not drawn; the rest of the page is unaffected.
      })
    return () => controller.abort()
  }, [key, name])
  return answer?.key === key ? answer.hostname : undefined
}

/**
 * One of a row's segments, under a tooltip. The tooltip's trigger hands its
 * child its own `data-state`, open or closed, which would overwrite the
 * toggle's on or off and leave every segment looking unpressed.
 */
function Segment(props: React.ComponentProps<typeof ToggleGroupItem>) {
  const own: typeof props & { "data-state"?: string } = { ...props }
  delete own["data-state"]
  return <ToggleGroupItem {...own} />
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
  existing,
  gated,
  fenced,
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
  /** The names already here, which a new one may not repeat. */
  existing: DomainValue[]
  /** The route's sign-in applies, which rules out a password. */
  gated: boolean
  /** The route has a saved address list, which rules out a tunnel. */
  fenced: boolean
  onAdd: (domain: DomainValue) => Promise<void>
}) {
  // Kept per project: a name half-typed for one is not the next one's.
  const project = useProject()
  const scope = `deploy.${project.projectId}.settings.domains.add`
  const [hostname, setHostname] = useSessionState(`${scope}.hostname`, "")
  const [https, setHttps] = useSessionState(`${scope}.https`, true)
  const [wantsTunnel, setTunnel] = useSessionState(`${scope}.tunnel`, false)
  const [wantsPassword, setGuarded] = useState(false)
  // What the field has been left holding, so a name still being typed is not
  // told off for being unfinished.
  const [left, setLeft] = useState(false)
  // The name a check could not get an answer for.
  const [unread, setUnread] = useState<string>()
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
    setLeft(false)
    setUnread(undefined)
  }

  const typed = hostnameFromInput(hostname)
  const problem = hostnameProblem(typed, existing)
  const wildcard = isWildcard(typed)
  const tunnelHeld = wildcard || fenced
  // A switch held off answers off, whatever was pressed before the name was
  // typed: the server would refuse both.
  const tunnel = wantsTunnel && !tunnelHeld
  const guarded = wantsPassword && !gated

  // The newest question asked. Leaving the field asks one and the tunnel
  // switch, pressed straight after, asks another; only the newest answer is
  // the sheet's, whichever arrives last.
  const asked = useRef(0)
  const check = async (through = tunnel) => {
    const value = typed
    if (!value || problem) return
    const question = ++asked.current
    setChecking(true)
    setUnread(undefined)
    try {
      const answer = await get<DeploymentHostnameSuggestion>("/deploy/hostname", {
        hostname: value,
        ...(through ? { tunnel: "1" } : {}),
      })
      if (question === asked.current) setSuggestion(answer)
    } catch {
      // The saved answer stays on screen, and a word says this one was not
      // read: pressing Check and hearing nothing reads as nothing being wrong.
      if (question === asked.current) setUnread(value)
    } finally {
      if (question === asked.current) setChecking(false)
    }
  }

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
  const typedProblem = passwordProblem(password)
  const userProblem = protectionUserProblem(username.trim())
  const incomplete =
    guarded && (!username.trim() || !password || Boolean(typedProblem || userProblem))

  const add = async () => {
    if (!typed || problem) return
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
      description={
        tunnel
          ? "Route a name on Cloudflare through the tunnel."
          : "Point a record at this server and check it before adding it."
      }
      size="md"
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
          <Button
            onClick={() => void add()}
            disabled={!typed || Boolean(problem) || incomplete}
            pending={saving}
          >
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
          error={left ? problem : undefined}
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
            answer ? (
              <Status
                key={answer.label}
                tone={answer.tone}
                label={answer.label}
                className="animate-rise"
              />
            ) : (
              unread === typed &&
              typed && (
                <Status key="unread" tone="unknown" label="Not checked" className="animate-rise" />
              )
            )
          }
        >
          <InputGroup>
            <InputGroupInput
              id="add-domain-hostname"
              value={hostname}
              onChange={(event) => {
                setHostname(event.target.value)
                setLeft(false)
              }}
              onBlur={() => {
                setHostname(typed)
                setLeft(true)
                void check()
              }}
              placeholder="app.example.com"
              autoComplete="off"
              spellCheck={false}
              aria-invalid={left && Boolean(problem)}
              className="font-mono"
            />
            <InputGroupAddon align="inline-end" className="gap-0 p-0">
              {!tunnel && <HttpsToggle checked={https} onChange={setHttps} />}
              <InputGroupButton
                aria-label="Check this hostname"
                pending={checking}
                disabled={!typed || Boolean(problem)}
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
            hint={
              !tunnelHeld
                ? "Cloudflare answers the name and HTTPS: no record points here, no port opens"
                : tunnelTip({ tunnel: false, wildcard, fenced })
            }
            checked={tunnel}
            disabled={tunnelHeld}
            onCheckedChange={(next) => {
              setTunnel(next)
              void check(next)
            }}
          />
          <OptionRow
            title="Ask visitors for a password"
            hint={passwordTip({ guarded: false, gated })}
            checked={guarded}
            disabled={gated}
            onCheckedChange={setGuarded}
          >
            <FieldRow>
              <Field
                label="User name"
                htmlFor="add-domain-user"
                error={userProblem}
                hint={!username && <FieldCheck met={false}>Required</FieldCheck>}
              >
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
                  <FieldChecks>
                    <FieldCheck met={password.length > 0 && !typedProblem}>
                      {typedProblem ?? `At least ${PASSWORD_MIN} characters`}
                    </FieldCheck>
                  </FieldChecks>
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

/**
 * Who issued a certificate and who renews it, as the reading's tooltip: facts
 * nobody acts on while it is valid, and what they look up when it is not.
 */
function certificateTip(route: DeploymentDomainRoute) {
  // An issuing intermediate's own name (R10) means nothing to a reader; who it
  // belongs to does, and the row's mark is drawn from the same reading.
  const issuer =
    issuerProduct(route.certificateIssuer) === "lets-encrypt"
      ? "Let's Encrypt"
      : route.certificateIssuer
  const parts = [
    issuer && `Issued by ${issuer}.`,
    route.certificateRenewedBy === "caddy" && "Renewed by Caddy.",
  ]
  return parts.filter(Boolean).join(" ")
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
  // A tunnel's requests reach the route from its connector, so a list would
  // refuse every visitor of the name or, with the connector on it, none.
  const tunnelled = configuration.domains.some((domain) => domain.tunnel)

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
          hint={
            tunnelled
              ? "Not with a name served through Cloudflare Tunnel: every request reaches this host from the connector, not the visitor's address. Fence that name with Cloudflare Access or a WAF rule."
              : "Anyone else is refused with 403, before the password or the sign-in is asked for. Empty lets everyone in."
          }
          error={refusal?.rule === "allowFrom" ? refusal.message : undefined}
          // An entry already saved stays removable, which is the way out.
          disabled={!canAdmin || (tunnelled && rules.allowFrom.length === 0)}
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
        // The cost, then which domains it is sent on: what the header is
        // was a sentence in front of both, on a switch named for it.
        hint={`Hard to undo. ${sentOn}${notOn}`}
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
        hint="A sign-in server checks every request; if it does not answer, neither does the site"
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
            <Segments
              label="Auth server"
              value={gate.provider}
              options={SSO_PROVIDERS.map((provider) => ({
                value: provider.value,
                label: provider.label,
              }))}
              onChange={(value) => update(chooseSsoProvider(gate, value))}
              fill
              className="flex-wrap"
              disabled={disabled}
            />
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
            <Segments
              label="Who signs in"
              value={gate.previewsOnly ? "previews" : "everywhere"}
              options={[
                { value: "everywhere", label: "Here and on previews" },
                { value: "previews", label: "Previews only" },
              ]}
              onChange={(value) => update({ previewsOnly: value === "previews" || undefined })}
              fill
              disabled={disabled}
            />
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
            hint="On saving, without a deployment; the addresses below are let past"
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

  // A page exists to edit once its code is saved: one switched on and not yet
  // saved has no file behind it.
  const editable = ERROR_PAGES.filter((code) => saved.errorPages.includes(code)).map(
    (code) => String(code) as DeploymentPageName,
  )

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
          state={maintenancePolicyState({
            retryAfter: retryAfter ?? undefined,
            bypassFrom: pages.bypassFrom,
          })}
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
          {canAdmin && (
            <div>
              <Button
                type="button"
                size="sm"
                variant="outline"
                onClick={() => setEditing("maintenance")}
              >
                Edit {PAGE_LABEL.maintenance}
              </Button>
            </div>
          )}
        </SettingSection>
        <SettingSection
          title="Error pages"
          state={errorPagesState(pages.errorPages, pages.interceptErrors)}
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
                    : "The application's own answers with those codes get these pages too"
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
          {canAdmin && editable.length > 0 && (
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
            An edited page is served from the next answer that uses it. Which pages the route
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
