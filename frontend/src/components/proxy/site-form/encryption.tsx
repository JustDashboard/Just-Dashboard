"use client"

import type { SiteSpec } from "@/lib/types"
import { OptionList, OptionRow } from "@/components/form"
import { useConfirm } from "@/components/confirm-dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  HSTS_MAX_AGES,
  HSTS_DEFAULT_MAX_AGE,
  HSTS_PRELOAD_MAX_AGE,
} from "@/components/proxy/site-form/hsts"
import { Field } from "@/components/proxy/site-form/fields"

/**
 * The HSTS policy's lifetime, whether it covers subdomains, and preload.
 * Preload is behind a confirmation because it outlives the header:
 * once a browser ships with the name built in, removing the header changes
 * nothing for months, and every subdomain has to serve HTTPS in the meantime.
 */
export function HSTSOptions({
  spec,
  set,
}: {
  spec: SiteSpec
  set: <K extends keyof SiteSpec>(key: K, value: SiteSpec[K]) => void
}) {
  const { confirm, dialog } = useConfirm()
  const maxAge = spec.hstsMaxAge || HSTS_DEFAULT_MAX_AGE
  const listed = HSTS_MAX_AGES.some((a) => a.value === maxAge)
  const domain = spec.domains[0] ?? "preload"
  const setMaxAge = (value: number) =>
    set("hstsMaxAge", value === HSTS_DEFAULT_MAX_AGE ? undefined : value)
  const askPreload = () =>
    confirm({
      title: "Ask browsers to preload HSTS",
      description: (
        <>
          The header will carry <code className="font-mono">preload</code>, which lets {domain} be
          submitted to the list browsers ship with. Once it is in, every subdomain must serve HTTPS,
          and leaving the list takes months of browser releases. The policy is raised to at least a
          year and covers subdomains, as the list requires.
        </>
      ),
      confirmLabel: "Turn on preload",
      action: async () => {
        if (maxAge < HSTS_PRELOAD_MAX_AGE) set("hstsMaxAge", HSTS_PRELOAD_MAX_AGE)
        set("hstsOwnNameOnly", undefined)
        set("hstsPreload", true)
      },
    })
  return (
    <div className="space-y-3">
      <Field
        label="Remember for"
        htmlFor="site-hsts-max-age"
        tip="Start short while checking every subdomain works over HTTPS, then raise it."
      >
        <Select value={String(maxAge)} onValueChange={(v) => setMaxAge(Number(v))}>
          <SelectTrigger id="site-hsts-max-age" className="w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            {!listed && <SelectItem value={String(maxAge)}>{`${maxAge} seconds`}</SelectItem>}
            {HSTS_MAX_AGES.map((a) => (
              <SelectItem
                key={a.value}
                value={String(a.value)}
                disabled={spec.hstsPreload && a.value < HSTS_PRELOAD_MAX_AGE}
              >
                {a.label}
              </SelectItem>
            ))}
          </SelectContent>
        </Select>
      </Field>
      <OptionList>
        <OptionRow
          title="Include subdomains"
          hint="Every name under this one must serve HTTPS too, including ones on other servers."
          checked={!spec.hstsOwnNameOnly}
          onCheckedChange={(v) => {
            set("hstsOwnNameOnly", v ? undefined : true)
            if (!v) set("hstsPreload", undefined)
          }}
        />
        <OptionRow
          title="Preload"
          hint="Lets the name be built into browsers. Needs a year and subdomains; very slow to undo."
          checked={Boolean(spec.hstsPreload)}
          onCheckedChange={(v) => (v ? askPreload() : set("hstsPreload", undefined))}
        />
      </OptionList>
      {dialog}
    </div>
  )
}
