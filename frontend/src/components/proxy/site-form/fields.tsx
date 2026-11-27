"use client"

import { createContext, useContext } from "react"
import type { Container, Listener } from "@/lib/types"
import { Field as FormField, FormSection, InfoTip } from "@/components/form"
import { ListField as SharedListField } from "@/components/list-field"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import { SettingSection } from "@/components/settings/setting-card"
import type { UpstreamOption } from "@/components/proxy/upstream-options"

/*
 * Every part of the site form draws its fields through these, so a field reads the same in the
 * sheet and in a site's Settings (`PageLayout`).
 */

/**
 * Whether the form is the Settings view of a site's page rather than the
 * sheet. A part opens as a page's section there, and the line under a field
 * that only explains it goes behind the ⓘ beside its label.
 */
export const PageLayout = createContext(false)

/**
 * A field of the form. `tip` is what the field is for, in a sentence: the
 * line under it in the sheet, where a first site is being made, and the ⓘ
 * beside its label in Settings, where it is read once. `hint` stays under
 * the field in both: it is what the field currently says — where the file is
 * saved, that nothing listens behind an address.
 */
export function Field({
  tip,
  ...props
}: React.ComponentProps<typeof FormField> & { tip?: string }) {
  const page = useContext(PageLayout)
  if (page) return <FormField {...props} info={props.info ?? tip} />
  return <FormField {...props} hint={props.hint ?? tip} />
}

/** A titled part of the form: a section of the page in Settings, of the sheet otherwise. */
export function Part({
  title,
  hint,
  children,
}: {
  title: string
  /** What the part is for; behind an ⓘ in Settings, where no sentence stands under a title. */
  hint?: string
  children: React.ReactNode
}) {
  const page = useContext(PageLayout)
  // What the part is for is a sentence nobody needs before acting, so it is
  // behind an ⓘ beside the title in both, never a line under it (PRM-16).
  const named = hint ? (
    <span className="inline-flex items-center gap-1.5">
      {title}
      <InfoTip label={`About ${title.toLowerCase()}`}>{hint}</InfoTip>
    </span>
  ) : (
    title
  )
  if (page) return <SettingSection title={named}>{children}</SettingSection>
  return <FormSection title={named}>{children}</FormSection>
}

/**
 * Single values, added one at a time: the shared `ListField`, with the
 * form's rule for the sentence that says what it is for (`Field`'s `tip`):
 * under it in the sheet, behind the ⓘ in Settings.
 *
 * @deprecated outside this form, import `ListField` from `@/components/list-field`.
 */
export function ListField({ hint, ...props }: React.ComponentProps<typeof SharedListField>) {
  const page = useContext(PageLayout)
  if (page) return <SharedListField {...props} info={props.info ?? hint} />
  return <SharedListField {...props} hint={hint} />
}

/** What the upstream pickers offer, read once for the whole form. */
export type Picker = {
  options: UpstreamOption[]
  listeners: Listener[] | undefined
  containers: Container[] | undefined
  loading: boolean
  failed: boolean
  onRetry: () => void
}

/** The site's setting, or on or off, for this path. */
export function OnOffField({
  id,
  label,
  value,
  onChange,
}: {
  id: string
  label: string
  value: "on" | "off" | undefined
  onChange: (value: "on" | "off" | undefined) => void
}) {
  return (
    <Field label={label} htmlFor={id}>
      <Select
        value={value ?? "site"}
        onValueChange={(v) => onChange(v === "on" || v === "off" ? v : undefined)}
      >
        <SelectTrigger id={id} className="w-full">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem value="site">The site&apos;s</SelectItem>
          <SelectItem value="on">On</SelectItem>
          <SelectItem value="off">Off</SelectItem>
        </SelectContent>
      </Select>
    </Field>
  )
}
