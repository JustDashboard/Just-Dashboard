"use client"

import { useCallback, useEffect, useState } from "react"
import Link from "next/link"
import { Code, Plus, Trash, Warning } from "@/components/icons"
import { notify } from "@/lib/toast"
import { get, post } from "@/lib/api"
import { cn } from "@/lib/utils"
import type {
  AuthFile,
  Certificate,
  DomainCheck,
  SiteLocation,
  SiteResult,
  SiteSpec,
} from "@/lib/types"
import { usePoll } from "@/hooks/use-poll"
import { CodeEditor } from "@/components/code-editor"
import { Field, FieldRow, FormNote, FormSection, OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Group, Pane } from "@/components/panel"
import { SidePanel } from "@/components/side-panel"
import { EmptyNote, Notice } from "@/components/state"
import { StatusDot } from "@/components/status-dot"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs"

/**
 * Putting a domain in front of a port, without writing nginx.
 *
 * This is the thing everyone actually does to a server — something is running
 * on 127.0.0.1:3000 and it needs to be app.example.com with a certificate —
 * and doing it by hand means knowing eight proxy_set_header lines by heart.
 * Getting one wrong produces a site that works until somebody logs in.
 *
 * The config is rendered on the *server* and shown live beside the form, for
 * the reason the Docker create form shows its docker run line: there is
 * exactly one implementation of what a spec means, the form is not a black
 * box, and the file it produces is ordinary nginx that can be committed and
 * edited by hand afterwards.
 *
 * The form is built from `components/form.tsx`: a `Field` is a label, a
 * control and one line; an `OptionRow` is a switch with its sentence. It had
 * its own Field, Toggle and Section before, which is how a form two clicks
 * from the databases' dialogs arrived at a different label size.
 */
export function SiteForm({
  open,
  editing,
  copyFrom,
  session,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  /** The site being edited, or null for a new one. */
  editing: string | null
  /** A site whose settings a new one starts from. */
  copyFrom?: string | null
  /** Bumped on every open, so a closed and reopened form never keeps a stale draft. */
  session?: number
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  // Keyed on the site so opening another never inherits the previous one's
  // buffer — saving that under the wrong name would be a real outage.
  return (
    <SiteFormBody
      key={`${editing ?? (copyFrom ? `copy:${copyFrom}` : "new")}:${session ?? 0}`}
      open={open}
      editing={editing}
      copyFrom={copyFrom ?? null}
      onOpenChange={onOpenChange}
      onSaved={onSaved}
    />
  )
}

const BLANK: SiteSpec = {
  name: "",
  domains: [],
  kind: "proxy",
  upstream: "http://127.0.0.1:3000",
  tls: false,
  forceHttps: true,
  hsts: true,
  http2: true,
  webSockets: true,
  gzip: true,
  blockExploits: true,
  securityHeaders: true,
  clientMaxBody: "50m",
  proxyTimeout: 60,
  allowFrom: [],
  denyFrom: [],
  accessLog: true,
  locations: [],
}

function SiteFormBody({
  open,
  editing,
  copyFrom,
  onOpenChange,
  onSaved,
}: {
  open: boolean
  editing: string | null
  copyFrom: string | null
  onOpenChange: (open: boolean) => void
  onSaved: () => void
}) {
  const [spec, setSpec] = useState<SiteSpec>(BLANK)
  const [domainText, setDomainText] = useState("")
  const [preview, setPreview] = useState("")
  const [warnings, setWarnings] = useState<string[]>([])
  const [previewError, setPreviewError] = useState("")
  const [managed, setManaged] = useState(true)
  const [busy, setBusy] = useState(false)
  const source = editing ?? copyFrom
  const [loaded, setLoaded] = useState(source === null)

  const set = useCallback(<K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => {
    setSpec((s) => ({ ...s, [key]: value }))
  }, [])

  // Load an existing site back into the form — as itself, or as the start of
  // a new one with the name, domains and certificate paths cleared, since
  // those three are the things a duplicate exists to change.
  useEffect(() => {
    if (!open || !source) return
    const controller = new AbortController()
    get<{ spec: SiteSpec; managed: boolean }>(
      `/proxy/sites/${encodeURIComponent(source)}`,
      undefined,
      controller.signal,
    )
      .then((r) => {
        if (copyFrom && !editing) {
          setSpec({
            ...BLANK,
            ...r.spec,
            name: "",
            domains: [],
            certPath: undefined,
            keyPath: undefined,
            managedAcme: false,
          })
          setDomainText("")
          setManaged(true)
        } else {
          setSpec({ ...BLANK, ...r.spec })
          setDomainText(r.spec.domains.join(" "))
          setManaged(r.managed)
        }
        setLoaded(true)
      })
      .catch((err) => !controller.signal.aborted && notify.error("Could not load the site", err))
    return () => controller.abort()
  }, [open, source, copyFrom, editing])

  // The live preview. Debounced, because it is a request per keystroke
  // otherwise and the answer only matters once typing stops.
  useEffect(() => {
    const controller = new AbortController()
    const ready = open && loaded && spec.domains.length > 0 && spec.name !== ""
    // Everything happens in the timeout, including clearing the preview. A
    // setState in the effect body itself is a cascading render, and the
    // not-ready case is the one that would fire on every keystroke.
    const timer = setTimeout(
      () => {
        if (!ready) {
          setPreview("")
          setWarnings([])
          setPreviewError("")
          return
        }
        post<{ content: string; warnings: string[] }>(
          "/proxy/sites/preview",
          { spec },
          { signal: controller.signal },
        )
          .then((r) => {
            setPreview(r.content)
            setWarnings(r.warnings)
            setPreviewError("")
          })
          .catch((err) => {
            if (controller.signal.aborted) return
            setPreview("")
            setPreviewError(String(err))
          })
      },
      ready ? 400 : 0,
    )
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [open, loaded, spec])

  // What the password field can point at, and whether the certificate the
  // form names is one this dashboard has seen. Both polled only while the
  // form is open and the field is in play.
  const authFiles = usePoll<AuthFile[]>(
    (signal) => get("/proxy/auth-files/", undefined, signal),
    0,
    [],
    { enabled: open },
  )
  const certs = usePoll<Certificate[]>(
    (signal) => get("/certificates/", undefined, signal),
    0,
    [],
    { enabled: open && spec.tls },
  )

  /**
   * The file name the server will accept: lowercase, starting with a letter or
   * a digit. Stripping the disallowed characters is not enough on its own —
   * `*.example.com` becomes `.example.com`, which the server refuses, and the
   * form has no name field to correct it in, so the first thing anybody
   * issuing a wildcard did was hit an error they could not fix.
   */
  const fileNameFor = (domain: string) =>
    domain
      .toLowerCase()
      .replace(/[^a-z0-9._-]/g, "")
      .replace(/^[^a-z0-9]+/, "")
      .slice(0, 64)

  const commitDomains = (text: string) => {
    setDomainText(text)
    const domains = text.split(/[\s,]+/).filter(Boolean)
    // A wildcard certificate is issued for the parent zone, so that is where
    // certbot puts it — /etc/letsencrypt/live/example.com, never
    // live/*.example.com, which is not a directory name at all.
    const lineage = domains[0]?.replace(/^\*\./, "")
    setSpec((s) => ({
      ...s,
      domains,
      // The file is named after the first domain unless somebody has already
      // typed a name. A site called "site-1" is one nobody can find later.
      name: s.name || (domains[0] ? fileNameFor(domains[0]) : ""),
      certPath:
        s.certPath || (lineage ? `/etc/letsencrypt/live/${lineage}/fullchain.pem` : undefined),
      keyPath: s.keyPath || (lineage ? `/etc/letsencrypt/live/${lineage}/privkey.pem` : undefined),
    }))
  }

  const save = async (reload: boolean) => {
    setBusy(true)
    try {
      const res = await post<SiteResult>("/proxy/sites/", {
        spec,
        enable: true,
        reload,
        overwrite: editing !== null,
      })
      notify.success(res.reloaded ? `${spec.name} is live` : `${spec.name} saved`, {
        description: res.reloaded
          ? undefined
          : "nginx has not reloaded yet, so the site is on disk but not serving.",
      })
      onSaved()
      onOpenChange(false)
    } catch (err) {
      notify.error("Not applied", err)
    } finally {
      setBusy(false)
    }
  }

  const ready = spec.domains.length > 0 && spec.name !== "" && preview !== ""
  const certKnown =
    !spec.tls || !spec.certPath || !certs.data || certs.data.some((c) => c.path === spec.certPath)
  const issueHref = `/proxy/certificates?issue=${encodeURIComponent(spec.domains.join(" "))}`

  return (
    <SidePanel
      open={open}
      onOpenChange={(o) => !busy && onOpenChange(o)}
      width="xl"
      title={editing ? `Edit ${editing}` : copyFrom ? `New site from ${copyFrom}` : "New site"}
      description={
        spec.domains.length > 0
          ? spec.domains.join(", ")
          : "A domain, where to send it, and whether it is encrypted"
      }
      bodyClassName="flex min-h-0 flex-1 flex-col gap-0 p-0 lg:flex-row"
      footer={
        <>
          <span className="mr-auto text-hint text-muted-foreground">
            Validated with nginx&rsquo;s own parser before it takes effect, and rolled back if the
            test fails.
          </span>
          <Button size="sm" variant="outline" onClick={() => save(false)} disabled={!ready || busy}>
            Save only
          </Button>
          <Button size="sm" onClick={() => save(true)} disabled={!ready || busy} pending={busy}>
            Save and reload
          </Button>
        </>
      }
    >
      <div className="min-h-0 flex-1 overflow-y-auto p-4 lg:w-[26rem] lg:shrink-0 lg:border-r lg:border-hairline">
        <div className="space-y-5">
          {editing && !managed && (
            <Notice tone="warning" icon={Warning} title="This file was written by hand">
              The form has read what it recognises. Saving replaces the file with what the form
              produces, so anything it could not represent will be lost — the previous version is
              kept as <code className="font-mono">.bak</code>.
            </Notice>
          )}

          <Field
            label="Domains"
            htmlFor="site-domains"
            hint={
              spec.name
                ? `Space-separated. Saved as ${spec.name} in nginx's site directory.`
                : "Space-separated. The first one names the file."
            }
          >
            <Input
              id="site-domains"
              value={domainText}
              onChange={(e) => commitDomains(e.target.value)}
              placeholder="app.example.com www.app.example.com"
              className="font-mono text-xs"
            />
          </Field>
          {spec.domains[0] && <DNSCheck domain={spec.domains[0]} />}

          <Field label="What it serves">
            <ToggleGroup
              type="single"
              value={spec.kind}
              onValueChange={(v) => v && set("kind", v as SiteSpec["kind"])}
              variant="outline"
              size="sm"
              className="w-full"
            >
              <ToggleGroupItem value="proxy" className="flex-1 text-hint">
                An app
              </ToggleGroupItem>
              <ToggleGroupItem value="static" className="flex-1 text-hint">
                Files
              </ToggleGroupItem>
              <ToggleGroupItem value="redirect" className="flex-1 text-hint">
                A redirect
              </ToggleGroupItem>
            </ToggleGroup>
          </Field>

          {spec.kind === "proxy" && (
            <Field
              label="Send it to"
              htmlFor="site-upstream"
              hint="Where the application is listening. Usually loopback on this machine."
            >
              <Input
                id="site-upstream"
                value={spec.upstream ?? ""}
                onChange={(e) => set("upstream", e.target.value)}
                placeholder="http://127.0.0.1:3000"
                className="font-mono text-xs"
              />
            </Field>
          )}
          {spec.kind === "static" && (
            <Field label="Directory" htmlFor="site-root" hint="The folder holding index.html.">
              <Input
                id="site-root"
                value={spec.root ?? ""}
                onChange={(e) => set("root", e.target.value)}
                placeholder="/var/www/site"
                className="font-mono text-xs"
              />
            </Field>
          )}
          {spec.kind === "redirect" && (
            <>
              <Field
                label="Redirect to"
                htmlFor="site-redirect"
                hint="The path and query are carried across."
              >
                <Input
                  id="site-redirect"
                  value={spec.redirectTo ?? ""}
                  onChange={(e) => set("redirectTo", e.target.value)}
                  placeholder="https://new.example.com"
                  className="font-mono text-xs"
                />
              </Field>
              <OptionList>
                <OptionRow
                  title="Permanent (301)"
                  hint="Browsers cache a permanent redirect more or less forever. Use 302 while you are still deciding."
                  checked={!!spec.permanent}
                  onCheckedChange={(v) => set("permanent", v)}
                />
              </OptionList>
            </>
          )}

          <FormSection title="Encryption">
            <OptionList>
              <OptionRow
                title="Serve over HTTPS"
                hint="Needs a certificate on disk. Issue one from the Certificates tab first."
                checked={spec.tls}
                onCheckedChange={(v) => set("tls", v)}
              >
                <div className="space-y-3">
                  <FieldRow>
                    <Field label="Certificate" htmlFor="site-cert">
                      <Input
                        id="site-cert"
                        value={spec.certPath ?? ""}
                        onChange={(e) => set("certPath", e.target.value)}
                        className="font-mono text-hint"
                      />
                    </Field>
                    <Field label="Private key" htmlFor="site-key">
                      <Input
                        id="site-key"
                        value={spec.keyPath ?? ""}
                        onChange={(e) => set("keyPath", e.target.value)}
                        className="font-mono text-hint"
                      />
                    </Field>
                  </FieldRow>
                  {!certKnown && (
                    <FormNote tone="warning">
                      No certificate is listed at this path. If it does not exist yet, nginx refuses
                      the site at reload —{" "}
                      <Link href={issueHref} className="underline underline-offset-2">
                        issue one for {spec.domains[0] ?? "these domains"} first
                      </Link>
                      .
                    </FormNote>
                  )}
                </div>
              </OptionRow>
              {spec.tls && (
                <>
                  <OptionRow
                    title="Send HTTP visitors to HTTPS"
                    hint="The certificate protects nobody who arrives on the unencrypted port."
                    checked={spec.forceHttps}
                    onCheckedChange={(v) => set("forceHttps", v)}
                  />
                  <OptionRow
                    title="HSTS"
                    hint="Tells the browser never to use plain HTTP for this name again. Hard to undo — a mistake sticks for six months."
                    checked={spec.hsts}
                    onCheckedChange={(v) => set("hsts", v)}
                  />
                  <OptionRow
                    title="HTTP/2"
                    hint="Faster for pages with many small assets."
                    checked={spec.http2}
                    onCheckedChange={(v) => set("http2", v)}
                  />
                </>
              )}
            </OptionList>
          </FormSection>

          {spec.kind === "proxy" && (
            <FormSection title="Behaviour">
              <OptionList>
                <OptionRow
                  title="WebSockets"
                  hint="Needed by anything with live updates: a chat, a terminal, a dashboard."
                  checked={spec.webSockets}
                  onCheckedChange={(v) => set("webSockets", v)}
                />
              </OptionList>
              <FieldRow>
                <Field
                  label="Upload limit"
                  htmlFor="site-body"
                  hint="nginx refuses a larger request body with a 413."
                >
                  <Input
                    id="site-body"
                    value={spec.clientMaxBody ?? ""}
                    onChange={(e) => set("clientMaxBody", e.target.value)}
                    placeholder="50m"
                    className="font-mono text-xs"
                  />
                </Field>
                <Field
                  label="Timeout"
                  htmlFor="site-timeout"
                  hint="Seconds nginx waits for the application to answer."
                >
                  <Input
                    id="site-timeout"
                    value={String(spec.proxyTimeout ?? 60)}
                    inputMode="numeric"
                    onChange={(e) => set("proxyTimeout", Number(e.target.value) || 0)}
                    className="font-mono text-xs"
                  />
                </Field>
              </FieldRow>
            </FormSection>
          )}

          <FormSection title="Hardening">
            <OptionList>
              <OptionRow
                title="Security headers"
                hint="nosniff, SAMEORIGIN and a referrer policy. Safe defaults for almost any site."
                checked={spec.securityHeaders}
                onCheckedChange={(v) => set("securityHeaders", v)}
              />
              <OptionRow
                title="Block common probes"
                hint="Refuses requests for dotfiles and backup extensions — the shapes scanners ask for all day."
                checked={spec.blockExploits}
                onCheckedChange={(v) => set("blockExploits", v)}
              />
              <OptionRow
                title="Compress responses"
                hint="gzip for text, JSON, scripts and SVG."
                checked={spec.gzip}
                onCheckedChange={(v) => set("gzip", v)}
              />
              <OptionRow
                title="Access log"
                hint="Off keeps the disk quiet; on is what you want when something goes wrong."
                checked={spec.accessLog}
                onCheckedChange={(v) => set("accessLog", v)}
              />
            </OptionList>
          </FormSection>

          <FormSection title="Who may reach it">
            <ListField
              id="site-allow"
              label="Allow only these"
              placeholder="10.0.0.0/8"
              values={spec.allowFrom}
              onChange={(v) => set("allowFrom", v)}
              hint="Filling this in refuses everything else. Include however you reach the site yourself."
            />
            <ListField
              id="site-deny"
              label="Deny"
              placeholder="203.0.113.0/24"
              values={spec.denyFrom}
              onChange={(v) => set("denyFrom", v)}
              hint="Exceptions, checked before the allow list. The fence at the end is written for you."
            />
            <Field
              label="Password file"
              htmlFor="site-auth"
              hint={
                authFiles.data && authFiles.data.length > 0
                  ? "One of the files managed below, or any htpasswd file. Leave empty for no password."
                  : "An htpasswd file. Create one in Password files below, or leave empty for no password."
              }
            >
              <Input
                id="site-auth"
                value={spec.basicAuthFile ?? ""}
                onChange={(e) => set("basicAuthFile", e.target.value)}
                placeholder="/etc/nginx/jd-auth/staging"
                list="site-auth-files"
                className="font-mono text-hint"
              />
              <datalist id="site-auth-files">
                {authFiles.data?.map((f) => (
                  <option key={f.path} value={f.path} />
                ))}
              </datalist>
            </Field>
          </FormSection>

          {spec.kind === "proxy" && (
            <FormSection
              title="Paths that go somewhere else"
              hint="Everything not matched by one of these goes to the site's main upstream."
            >
              <LocationsField locations={spec.locations} onChange={(v) => set("locations", v)} />
            </FormSection>
          )}

          <FormSection title="Anything else">
            <Field
              label="Extra configuration"
              htmlFor="site-custom"
              hint="Added verbatim inside the server block."
            >
              <Textarea
                id="site-custom"
                value={spec.custom ?? ""}
                onChange={(e) => set("custom", e.target.value)}
                rows={4}
                className="font-mono text-hint"
                placeholder="# valid nginx directives"
              />
            </Field>
          </FormSection>
        </div>
      </div>

      <div className="flex min-h-0 min-w-0 flex-1 flex-col gap-3 p-4">
        <Tabs defaultValue="preview" className="flex min-h-0 flex-1 flex-col gap-3">
          <TabsList>
            <TabsTrigger value="preview">
              <Code className="size-3.5" />
              nginx config
            </TabsTrigger>
            <TabsTrigger value="notes">
              Notes
              {warnings.length > 0 && (
                <span className="numeric ml-1 text-hint font-medium text-warning">
                  {warnings.length}
                </span>
              )}
            </TabsTrigger>
          </TabsList>
          <TabsContent value="preview" className="min-h-0 flex-1">
            <Pane className="h-full">
              {previewError ? (
                <EmptyNote className="my-auto text-destructive">{previewError}</EmptyNote>
              ) : preview ? (
                <CodeEditor className="h-full" language="ini" value={preview} readOnly />
              ) : (
                <EmptyNote className="my-auto">
                  Enter a domain and the config appears here, rendered by the server that will write
                  it.
                </EmptyNote>
              )}
            </Pane>
          </TabsContent>
          <TabsContent value="notes" className="min-h-0 flex-1 overflow-y-auto">
            {warnings.length === 0 ? (
              <p className="flex items-center gap-2.5 py-2 text-body text-muted-foreground">
                <StatusDot tone="running" />
                Every setting here is one this dashboard would have chosen.
              </p>
            ) : (
              <ul className="divide-y divide-hairline">
                {warnings.map((warning) => (
                  <li key={warning} className="flex items-start gap-2.5 py-2.5 text-body">
                    <StatusDot tone="warning" className="mt-1.5" />
                    <span className="min-w-0 leading-relaxed">{warning}</span>
                  </li>
                ))}
              </ul>
            )}
          </TabsContent>
        </Tabs>
      </div>
    </SidePanel>
  )
}

/**
 * Does the domain point here yet?
 *
 * The first question of every reverse-proxy setup and the cause of most of the
 * failures: certbot cannot prove control of a name that resolves somewhere
 * else, and the error it gives says "challenge failed" rather than "your DNS
 * is not updated yet".
 */
function DNSCheck({ domain }: { domain: string }) {
  const [check, setCheck] = useState<DomainCheck | null>(null)
  const [loading, setLoading] = useState(false)

  useEffect(() => {
    const controller = new AbortController()
    const timer = setTimeout(() => {
      setLoading(true)
      get<DomainCheck>("/certificates/dns", { domain }, controller.signal)
        .then(setCheck)
        .catch(() => setCheck(null))
        .finally(() => !controller.signal.aborted && setLoading(false))
    }, 600)
    return () => {
      clearTimeout(timer)
      controller.abort()
    }
  }, [domain])

  if (loading && !check) {
    return <FormNote className="-mt-3">Checking where {domain} points…</FormNote>
  }
  if (!check) return null
  return (
    <FormNote
      className="-mt-3"
      tone={
        check.pointsHere
          ? "success"
          : // A host behind provider NAT has no address of its own to compare
            // against, so "cannot tell" is muted like the CDN case rather than
            // warned about — the domain is very probably fine.
            check.behindProxy || !check.hostAddressesKnown
            ? "default"
            : "warning"
      }
    >
      {check.summary}
    </FormNote>
  )
}

function ListField({
  id,
  label,
  placeholder,
  values,
  onChange,
  hint,
}: {
  id: string
  label: string
  placeholder: string
  values: string[]
  onChange: (values: string[]) => void
  hint?: string
}) {
  const [draft, setDraft] = useState("")
  const add = () => {
    if (!draft.trim()) return
    onChange([...values, draft.trim()])
    setDraft("")
  }
  return (
    <Field label={label} htmlFor={id} hint={hint}>
      <div className="space-y-2">
        {values.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {values.map((value, i) => (
              <Tag key={`${value}-${i}`} mono className="gap-1.5 pr-0.5">
                {value}
                <IconAction
                  label={`Remove ${value}`}
                  size="icon-xs"
                  className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                  onClick={() => onChange(values.filter((_, j) => j !== i))}
                >
                  <Trash />
                </IconAction>
              </Tag>
            ))}
          </div>
        )}
        <div className="flex gap-2">
          <Input
            id={id}
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") {
                e.preventDefault()
                add()
              }
            }}
            placeholder={placeholder}
            className="font-mono text-xs"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={add}
            disabled={!draft.trim()}
            aria-label={`Add to ${label.toLowerCase()}`}
          >
            <Plus className="size-3.5" />
          </Button>
        </div>
      </div>
    </Field>
  )
}

/**
 * Extra paths, each sent somewhere other than the site's default.
 *
 * The commonest reverse-proxy layout after "one app on one domain" is two:
 * /api to a backend and everything else to a static build, or /ws to a socket
 * server. The renderer has always supported it and the form did not, which
 * meant the one arrangement past the simplest sent people to the raw editor.
 *
 * nginx matches the longest prefix regardless of order, so these are rendered
 * before the catch-all purely because that is the order a reader expects.
 */
function LocationsField({
  locations,
  onChange,
}: {
  locations: SiteLocation[]
  onChange: (locations: SiteLocation[]) => void
}) {
  const update = (i: number, patch: Partial<SiteLocation>) =>
    onChange(locations.map((loc, j) => (j === i ? { ...loc, ...patch } : loc)))

  return (
    <div className="space-y-2">
      {locations.map((loc, i) => (
        <Group key={i} className={cn("space-y-2")}>
          <div className="flex items-center gap-2">
            <Input
              value={loc.path}
              onChange={(e) => update(i, { path: e.target.value })}
              placeholder="/api"
              aria-label="Path"
              className="font-mono text-xs"
            />
            <IconAction
              label={`Remove ${loc.path || "location"}`}
              className="text-destructive"
              onClick={() => onChange(locations.filter((_, j) => j !== i))}
            >
              <Trash />
            </IconAction>
          </div>
          <Input
            value={loc.upstream ?? ""}
            onChange={(e) => update(i, { upstream: e.target.value, root: "" })}
            placeholder="http://127.0.0.1:4000 — or leave empty and give a folder"
            aria-label="Upstream"
            className="font-mono text-xs"
          />
          {!loc.upstream && (
            <Input
              value={loc.root ?? ""}
              onChange={(e) => update(i, { root: e.target.value })}
              placeholder="/var/www/assets"
              aria-label="Folder"
              className="font-mono text-xs"
            />
          )}
          <label className="flex items-center gap-2 text-hint text-muted-foreground">
            <Checkbox
              checked={loc.webSockets}
              onCheckedChange={(v) => update(i, { webSockets: Boolean(v) })}
            />
            WebSockets on this path
          </label>
        </Group>
      ))}
      <Button
        size="sm"
        variant="outline"
        onClick={() => onChange([...locations, { path: "", upstream: "", webSockets: false }])}
      >
        <Plus className="size-3.5" />
        Add a path
      </Button>
    </div>
  )
}
