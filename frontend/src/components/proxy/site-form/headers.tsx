"use client"

import { useState } from "react"
import { Plus, Trash } from "@/components/icons"
import type {
  CORSMethod,
  CSPDirective,
  HeaderValue,
  PermissionRule,
  SiteCORS,
  SiteCSP,
  SiteHeaders,
  SiteSpec,
} from "@/lib/types"
import { OptionList, OptionRow } from "@/components/form"
import { IconAction } from "@/components/icon-action"
import { Tag } from "@/components/tag"
import { Button } from "@/components/ui/button"
import { Checkbox } from "@/components/ui/checkbox"
import { Input } from "@/components/ui/input"
import { Textarea } from "@/components/ui/textarea"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { Field, Part, ListField } from "@/components/proxy/site-form/fields"

const CORS_METHODS: CORSMethod[] = ["GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"]

const CSP_KEYWORDS = [
  "self",
  "none",
  "unsafe-inline",
  "unsafe-eval",
  "unsafe-hashes",
  "strict-dynamic",
  "report-sample",
  "wasm-unsafe-eval",
  "inline-speculation-rules",
]

const CSP_PRESET = "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'self'"

/** The features the form offers; one read from the file is listed too. */
const PERMISSION_FEATURES = ["camera", "microphone", "geolocation", "payment", "usb", "fullscreen"]

const PERMISSION_LABEL: Record<PermissionRule["allow"], string> = {
  none: "Nobody",
  self: "This site",
  all: "Everyone",
}

function cspQuoted(source: string): boolean {
  return CSP_KEYWORDS.includes(source) || /^(nonce|sha256|sha384|sha512)-/.test(source)
}

/** The policy as it is written in the header, keywords in their quotes. */
function formatCSP(csp: SiteCSP): string {
  return csp.directives
    .map((d) => [d.name, ...(d.sources ?? []).map((s) => (cspQuoted(s) ? `'${s}'` : s))].join(" "))
    .join("; ")
}

/**
 * The typed policy as directives. A keyword's quotes come off, since the
 * renderer writes them; anything else quoted is kept as typed, so the save
 * says what is wrong with it.
 */
function parseCSP(text: string): CSPDirective[] {
  return text
    .split(";")
    .map((part) => part.trim().split(/\s+/).filter(Boolean))
    .filter((words) => words.length > 0)
    .map(([name, ...sources]) => ({
      name: name.toLowerCase(),
      sources: sources.map((s) => {
        const bare = s.replace(/^'(.*)'$/, "$1")
        return cspQuoted(bare) ? bare : s
      }),
    }))
}

/** Drops a headers section that sets nothing, so the spec says none. */
function emptyHeaders(h: SiteHeaders): boolean {
  return (
    !h.request?.length &&
    !h.response?.length &&
    !h.hide?.length &&
    !h.frameOptions &&
    !h.csp &&
    !h.permissions?.length &&
    !h.cors
  )
}

/**
 * Headers added to answers and to what the application is sent, and the
 * browser policies: CORS for an API, a CSP that can be tried report-only
 * first, Permissions-Policy and X-Frame-Options.
 */
export function HeadersSection({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const h = spec.headers ?? {}
  const proxy = spec.kind === "proxy"
  const setHeaders = (patch: Partial<SiteHeaders>) => {
    const next = { ...h, ...patch }
    set("headers", emptyHeaders(next) ? undefined : next)
  }
  const cors = h.cors
  const setCors = (patch: Partial<SiteCORS>) =>
    setHeaders({ cors: { origins: [], methods: ["GET", "POST"], ...cors, ...patch } })
  const [cspText, setCspText] = useState(() => (h.csp ? formatCSP(h.csp) : ""))
  const permissions = h.permissions ?? []
  const features = [
    ...new Set([...PERMISSION_FEATURES, ...permissions.map((rule) => rule.feature)]),
  ]
  const setPermission = (feature: string, allow: PermissionRule["allow"] | "unset") => {
    const rest = permissions.filter((rule) => rule.feature !== feature)
    setHeaders({ permissions: allow === "unset" ? rest : [...rest, { feature, allow }] })
  }

  return (
    <Part
      title="Headers"
      hint="What every answer carries, what the application is sent, and the policies browsers follow."
    >
      <OptionList>
        {spec.kind !== "redirect" && (
          <OptionRow
            title="CORS for an API"
            hint="Pages on the origins below may call this site from a browser. A preflight is answered by nginx with 204; other origins get no Access-Control-Allow-Origin."
            checked={Boolean(cors)}
            onCheckedChange={(on) =>
              setHeaders({
                cors: on
                  ? { origins: [], methods: ["GET", "POST", "PUT", "PATCH", "DELETE"] }
                  : undefined,
              })
            }
          >
            {cors && (
              <div className="space-y-2">
                <ListField
                  id="site-cors-origins"
                  label="Allowed origins"
                  placeholder="https://app.example.com"
                  values={cors.origins}
                  onChange={(origins) => setCors({ origins })}
                  hint="Scheme and host, with a port if not the default, and no path. * alone allows any origin, without credentials."
                />
                <Field label="Methods">
                  <div className="flex flex-wrap gap-x-4 gap-y-2">
                    {CORS_METHODS.map((method) => (
                      <label
                        key={method}
                        className="flex items-center gap-2 font-mono text-hint text-muted-foreground"
                      >
                        <Checkbox
                          checked={cors.methods.includes(method)}
                          onCheckedChange={(v) =>
                            setCors({
                              methods: v
                                ? CORS_METHODS.filter(
                                    (m) => m === method || cors.methods.includes(m),
                                  )
                                : cors.methods.filter((m) => m !== method),
                            })
                          }
                        />
                        {method}
                      </label>
                    ))}
                  </div>
                </Field>
                <ListField
                  id="site-cors-headers"
                  label="Allowed request headers"
                  placeholder="Content-Type"
                  values={cors.headers ?? []}
                  onChange={(headers) => setCors({ headers })}
                  hint="Empty allows whichever headers the browser asks for."
                />
                <label className="flex items-center gap-2 text-hint text-muted-foreground">
                  <Checkbox
                    checked={cors.credentials ?? false}
                    onCheckedChange={(v) => setCors({ credentials: Boolean(v) })}
                  />
                  Allow credentials: the browser sends cookies and HTTP auth along
                </label>
              </div>
            )}
          </OptionRow>
        )}
        <OptionRow
          title="Content-Security-Policy"
          hint="Which scripts, styles, frames and connections the site's pages may use. Try it report-only first: browsers then report what it would block and block nothing."
          checked={Boolean(h.csp)}
          onCheckedChange={(on) => {
            setCspText(on ? CSP_PRESET : "")
            setHeaders({
              csp: on ? { directives: parseCSP(CSP_PRESET), reportOnly: true } : undefined,
            })
          }}
        >
          {h.csp && (
            <div className="space-y-2">
              <Textarea
                aria-label="Content-Security-Policy"
                value={cspText}
                onChange={(e) => {
                  setCspText(e.target.value)
                  setHeaders({ csp: { ...h.csp, directives: parseCSP(e.target.value) } })
                }}
                rows={3}
                className="font-mono text-xs"
              />
              <label className="flex items-center gap-2 text-hint text-muted-foreground">
                <Checkbox
                  checked={h.csp.reportOnly ?? false}
                  onCheckedChange={(v) =>
                    h.csp && setHeaders({ csp: { ...h.csp, reportOnly: Boolean(v) } })
                  }
                />
                Report only: send it as Content-Security-Policy-Report-Only
              </label>
            </div>
          )}
        </OptionRow>
        <OptionRow
          title="Permissions-Policy"
          hint="Which browser features the site's pages, and the frames in them, may ask for."
          checked={permissions.length > 0}
          onCheckedChange={(on) =>
            setHeaders({
              permissions: on
                ? ["camera", "microphone", "geolocation", "payment"].map((feature) => ({
                    feature,
                    allow: "none" as const,
                  }))
                : [],
            })
          }
        >
          {permissions.length > 0 && (
            <div className="grid gap-2 sm:grid-cols-2">
              {features.map((feature) => (
                <Field key={feature} label={feature} htmlFor={`site-permission-${feature}`}>
                  <Select
                    value={permissions.find((rule) => rule.feature === feature)?.allow ?? "unset"}
                    onValueChange={(v) =>
                      setPermission(feature, v as PermissionRule["allow"] | "unset")
                    }
                  >
                    <SelectTrigger id={`site-permission-${feature}`} className="w-full">
                      <SelectValue />
                    </SelectTrigger>
                    <SelectContent>
                      <SelectItem value="unset">Browser default</SelectItem>
                      {(["none", "self", "all"] as const).map((allow) => (
                        <SelectItem key={allow} value={allow}>
                          {PERMISSION_LABEL[allow]}
                        </SelectItem>
                      ))}
                    </SelectContent>
                  </Select>
                </Field>
              ))}
            </div>
          )}
        </OptionRow>
      </OptionList>
      <Field
        label="X-Frame-Options"
        htmlFor="site-frame-options"
        tip="Whether other sites may show this one in a frame. A CSP's frame-ancestors, where set, is what current browsers follow."
      >
        <Select
          value={h.frameOptions || "unset"}
          onValueChange={(v) =>
            setHeaders({ frameOptions: v === "unset" ? undefined : (v as "deny" | "sameorigin") })
          }
        >
          <SelectTrigger id="site-frame-options" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem value="unset">
              {spec.securityHeaders ? "SAMEORIGIN, from Security headers" : "Not sent"}
            </SelectItem>
            <SelectItem value="sameorigin">SAMEORIGIN: only this site</SelectItem>
            <SelectItem value="deny">DENY: never in a frame</SelectItem>
          </SelectContent>
        </Select>
      </Field>
      <HeaderListField
        id="site-response-headers"
        label="Response headers"
        values={h.response ?? []}
        onChange={(response) => setHeaders({ response })}
        hint={
          proxy
            ? "Added to every answer. nginx adds a header beside the application's own, so hide the application's below if it sends the same one."
            : "Added to every answer, errors included."
        }
      />
      {proxy && (
        <>
          <HeaderListField
            id="site-request-headers"
            label="Request headers to the application"
            values={h.request ?? []}
            onChange={(request) => setHeaders({ request })}
            hint="Plain text, or one nginx variable such as $ssl_client_s_dn. An empty value stops the visitor's header of that name reaching the application."
          />
          <ListField
            id="site-hide-headers"
            label="Hide from the application's responses"
            placeholder="X-Powered-By"
            values={h.hide ?? []}
            onChange={(hide) => setHeaders({ hide })}
            hint="nginx already drops Server and Date. With CORS on, the application's own CORS headers are hidden too."
          />
        </>
      )}
    </Part>
  )
}

/** Name and value pairs, added one at a time. The deployment Domains page uses it too. */
export function HeaderListField({
  id,
  label,
  values,
  onChange,
  hint,
  error,
  disabled,
}: {
  id: string
  label: string
  values: HeaderValue[]
  onChange: (values: HeaderValue[]) => void
  hint?: string
  error?: string
  /** Read-only: the values without the controls that change them. */
  disabled?: boolean
}) {
  const [name, setName] = useState("")
  const [value, setValue] = useState("")
  const add = () => {
    if (!name.trim()) return
    onChange([...values, { name: name.trim(), value: value.trim() }])
    setName("")
    setValue("")
  }
  return (
    <Field label={label} htmlFor={id} tip={hint} error={error}>
      <div className="space-y-2">
        {values.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5">
            {values.map((header, i) => (
              <Tag key={`${header.name}-${i}`} mono className="gap-1.5 pr-0.5">
                {header.name}: {header.value || "(empty)"}
                {!disabled && (
                  <IconAction
                    type="button"
                    label={`Remove ${header.name}`}
                    size="icon-xs"
                    className="size-4 text-muted-foreground hover:text-destructive [&_svg:not([class*='size-'])]:size-2.5"
                    onClick={() => onChange(values.filter((_, j) => j !== i))}
                  >
                    <Trash />
                  </IconAction>
                )}
              </Tag>
            ))}
          </div>
        )}
        {!disabled && (
          <div className="flex gap-2">
            <Input
              id={id}
              aria-label={`${label}: name`}
              value={name}
              onChange={(e) => setName(e.target.value)}
              // Enter in a field submits the form around it, which would save
              // the page with this header half typed.
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault()
                  add()
                }
              }}
              placeholder="X-Robots-Tag"
              className="font-mono text-xs"
            />
            <Input
              aria-label={`${label}: value`}
              value={value}
              onChange={(e) => setValue(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") {
                  e.preventDefault()
                  add()
                }
              }}
              placeholder="noindex"
              className="font-mono text-xs"
            />
            <Button
              type="button"
              size="sm"
              variant="outline"
              onClick={add}
              disabled={!name.trim()}
              aria-label={`Add to ${label.toLowerCase()}`}
            >
              <Plus className="size-3.5" />
            </Button>
          </div>
        )}
      </div>
    </Field>
  )
}
