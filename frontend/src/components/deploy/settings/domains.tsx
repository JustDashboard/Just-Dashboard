"use client"

import { useState } from "react"
import Link from "next/link"
import { useRouter } from "next/navigation"
import { useSessionState } from "@/lib/view-state"
import {
  ArrowUpRight,
  Copy,
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
import { ApiError, get, refusedIndex } from "@/lib/api"
import { copyText } from "@/lib/clipboard"
import { plural } from "@/lib/format"
import { notify } from "@/lib/toast"
import { cn } from "@/lib/utils"
import { useAuth } from "@/hooks/use-auth"
import type {
  DeploymentDomainRoute,
  DeploymentEnvironmentConfiguration,
  DeploymentHostnameSuggestion,
} from "@/lib/types"
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
  InputGroup,
  InputGroupAddon,
  InputGroupButton,
  InputGroupInput,
} from "@/components/ui/input-group"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { HttpsToggle } from "@/components/deploy/https-toggle"
import { CertificateStatus, ROUTE_LABEL, ROUTE_TONE } from "@/components/deploy/vocabulary"
import {
  SettingForm,
  SettingSection,
  SettingsPage,
  settingStatus,
} from "@/components/deploy/settings/setting-card"
import { useConfiguration, useSettingDraft } from "@/components/deploy/settings/use-configuration"
import { OwnershipSelect } from "@/components/deploy/settings/mounts"
import { useProject } from "@/components/deploy/project-context"

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
      {(configuration) => <DomainsForm configuration={configuration} save={state.save} />}
    </SettingsPage>
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
    if (canAdmin)
      verbs.push({
        key: "remove",
        label: "Remove",
        icon: Trash,
        danger: true,
        run: () => setDomains(domains.filter((_, i) => i !== index)),
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
              <Link href="/security/firewall" className="underline underline-offset-4">
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
  const httpsChanging = Boolean(route) && domain.https !== route?.https
  const liveCertificate = route && !unsaved && !httpsChanging ? route : undefined

  const issuer = issuerProduct(liveCertificate?.certificateIssuer)

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
          value={[domain.https && "https", protection && "password"].filter(
            (value): value is string => Boolean(value),
          )}
          onValueChange={(values) =>
            onChange({
              https: values.includes("https"),
              protection: values.includes("password")
                ? (protection ?? { username: "", password: "" })
                : undefined,
            })
          }
          disabled={!canAdmin}
          className="shrink-0"
        >
          <ToggleGroupItem
            value="https"
            aria-label={`HTTPS on ${host}`}
            className="h-11 gap-1.5 text-body data-[state=off]:text-muted-foreground sm:h-9"
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
  return { tone: "warning", label: "HTTPS needs attention" }
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
  const [guarded, setGuarded] = useState(false)
  const [username, setUsername] = useState("")
  const [password, setPassword] = useState("")
  const [checking, setChecking] = useState(false)
  const [saving, setSaving] = useState(false)
  const [suggestion, setSuggestion] = useState<DeploymentHostnameSuggestion>()

  const reset = () => {
    setHostname("")
    setHttps(true)
    setGuarded(false)
    setUsername("")
    setPassword("")
    setSuggestion(undefined)
  }

  const check = async () => {
    const value = hostname.trim().toLowerCase()
    if (!value) return
    setChecking(true)
    try {
      setSuggestion(
        await get<DeploymentHostnameSuggestion>("/deploy/hostname", { hostname: value }),
      )
    } catch {
      // The saved answer stays on screen; the deploy itself is what settles this.
    } finally {
      setChecking(false)
    }
  }

  const typed = hostname.trim().toLowerCase()
  const matches = suggestion?.hostname.toLowerCase() === typed
  const answer = https && matches && suggestion ? certificateReading(suggestion) : undefined
  const method = matches ? suggestion?.certificateMethod : undefined
  const glyph = method ? METHOD_PRODUCT[method] : undefined
  const ordered = https && suggestion && !suggestion.covered ? method : undefined
  const record = matches && suggestion?.address ? `A  ${typed}  →  ${suggestion.address}` : ""
  const incomplete = guarded && (!username.trim() || password.length < 8)

  const add = async () => {
    if (!typed) return
    setSaving(true)
    try {
      await onAdd({
        hostname: typed,
        https,
        ownership: "managed",
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
          info="Point a DNS record for the name at this server first. Check reads where it points and whether a certificate can be issued for it."
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
              <HttpsToggle checked={https} onChange={setHttps} />
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

        {https && matches && suggestion && !suggestion.covered && !method && (
          <Notice
            tone="warning"
            icon={Warning}
            title="Automatic HTTPS needs attention"
            className="animate-rise"
          >
            {suggestion.certificateIssue ?? "Certificate readiness could not be confirmed."}
          </Notice>
        )}

        <OptionList>
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
