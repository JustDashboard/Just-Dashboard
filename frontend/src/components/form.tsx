"use client"

import { Copy } from "@/components/icons"
import { cn } from "@/lib/utils"
import { copyText } from "@/lib/clipboard"
import type { Tone } from "@/components/tone"
import { Well } from "@/components/panel"
import { Button } from "@/components/ui/button"
import { Label } from "@/components/ui/label"
import { Switch } from "@/components/ui/switch"

/**
 * The form vocabulary for a task surface.
 *
 * Every dialog in the databases section — create a table, add a column,
 * connect a server, import a file, edit a row — had assembled its own form out
 * of `Label`, `Input` and a `space-y-1.5` div, and between them they had
 * arrived at three label sizes, two input heights, four spellings of a hint
 * and no way at all of writing an error next to the field that caused it. A
 * reader opening two of those dialogs in a row saw two products.
 *
 * One vocabulary: a `Field` is a label, a control and one line under it; a
 * `FieldRow` puts two or three side by side; a `FormSection` opens a part of a
 * longer form with an eyebrow and a hairline; an `OptionRow` is a switch with
 * its sentence, because a checkbox labelled "Stop on error" asks the reader to
 * guess what an error stops; `FormFacts` states what the form operates on; and
 * `Statement` shows the SQL a form is about to run, which is the one thing a
 * schema-editing dialog owes the person pressing its button.
 */

/** One labelled control, its hint, and — when the value is wrong — why. */
export function Field({
  label,
  htmlFor,
  hint,
  error,
  trailing,
  className,
  children,
}: {
  label: React.ReactNode
  htmlFor?: string
  /** What goes here, in one line. Replaced by the error while there is one. */
  hint?: React.ReactNode
  error?: React.ReactNode
  /** A control at the label's right edge: a null switch, a "leave blank" note. */
  trailing?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <div className="flex min-w-0 items-center justify-between gap-3">
        <Label htmlFor={htmlFor} className="min-w-0 text-body font-medium">
          {label}
        </Label>
        {trailing && <div className="flex shrink-0 items-center gap-2">{trailing}</div>}
      </div>
      {children}
      {error ? (
        <p role="alert" className="text-hint leading-relaxed text-destructive">
          {error}
        </p>
      ) : (
        hint && <p className="text-hint leading-relaxed text-muted-foreground">{hint}</p>
      )}
    </div>
  )
}

/** Two or three fields on one line; one under another on a phone. */
export function FieldRow({
  columns = 2,
  className,
  ...props
}: React.ComponentProps<"div"> & { columns?: 2 | 3 }) {
  return (
    <div
      className={cn(
        "grid gap-3 [&>*]:min-w-0",
        columns === 2 ? "sm:grid-cols-2" : "sm:grid-cols-3",
        className,
      )}
      {...props}
    />
  )
}

/**
 * A titled part of a longer form. The eyebrow and hairline are the same marks
 * a page uses to open a section, at the size a dialog has room for.
 */
export function FormSection({
  title,
  hint,
  actions,
  className,
  children,
}: {
  title: React.ReactNode
  hint?: React.ReactNode
  actions?: React.ReactNode
  className?: string
  children: React.ReactNode
}) {
  return (
    <section className={cn("min-w-0 space-y-3", className)}>
      <div className="flex min-w-0 flex-wrap items-end justify-between gap-x-4 gap-y-1 border-b border-hairline pb-2">
        <div className="min-w-0">
          <p className="eyebrow">{title}</p>
          {hint && <p className="mt-0.5 text-hint text-muted-foreground">{hint}</p>}
        </div>
        {actions && <div className="flex shrink-0 items-center gap-1.5">{actions}</div>}
      </div>
      {children}
    </section>
  )
}

/** A run of `OptionRow`s, separated by hairlines. */
export function OptionList({ className, ...props }: React.ComponentProps<"div">) {
  return <div className={cn("min-w-0 divide-y divide-hairline", className)} {...props} />
}

/**
 * A switch with its sentence.
 *
 * "Stop on error" as a checkbox label asks the reader to guess what an error
 * stops and what happens to the rows before it. The title names the option and
 * the hint says what choosing it does, which is the only form most of these
 * are usable in. A `danger` tone marks the one option in a list that loses
 * something.
 */
export function OptionRow({
  title,
  hint,
  checked,
  onCheckedChange,
  disabled,
  tone = "default",
  children,
}: {
  title: React.ReactNode
  hint?: React.ReactNode
  checked: boolean
  onCheckedChange: (checked: boolean) => void
  disabled?: boolean
  tone?: Tone
  /** An extra control the option reveals — a field that only matters when it is on. */
  children?: React.ReactNode
}) {
  return (
    <div className="py-2.5 first:pt-0 last:pb-0">
      <label className="flex min-w-0 items-start justify-between gap-4">
        <span className="min-w-0">
          <span
            className={cn(
              "block text-body font-medium",
              tone === "danger" && "text-destructive",
              tone === "warning" && "text-warning",
            )}
          >
            {title}
          </span>
          {hint && (
            <span className="block text-hint leading-relaxed text-muted-foreground">{hint}</span>
          )}
        </span>
        <Switch
          size="sm"
          className="mt-0.5"
          checked={checked}
          onCheckedChange={onCheckedChange}
          disabled={disabled}
        />
      </label>
      {children && checked && <div className="mt-2.5">{children}</div>}
    </div>
  )
}

/**
 * What the form operates on, said as data under the title rather than as a
 * sentence: the schema a table lands in, the address a server was found at.
 */
export function FormFacts({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      className={cn(
        "flex min-w-0 flex-wrap items-center gap-x-4 gap-y-1 text-hint text-muted-foreground",
        className,
      )}
      {...props}
    />
  )
}

export function FormFact({
  label,
  mono,
  children,
}: {
  label: React.ReactNode
  /** The value is a literal from the host — a name, an address, a path. */
  mono?: boolean
  children: React.ReactNode
}) {
  return (
    <span className="inline-flex min-w-0 items-baseline gap-1.5">
      <span className="shrink-0">{label}</span>
      <span className={cn("min-w-0 truncate text-foreground", mono && "font-mono")}>
        {children}
      </span>
    </span>
  )
}

/**
 * The statement a form will run, shown before it runs.
 *
 * A DDL form that hides its SQL asks the operator to trust a black box with
 * their schema; showing it costs a few lines and turns the form into something
 * that can also be learned from. The server renders the real statement in the
 * engine's own dialect — this is drawn from the same fields, so the shape is
 * right even where a keyword differs.
 */
export function Statement({
  label = "Statement",
  sql,
  placeholder,
  className,
}: {
  label?: React.ReactNode
  /** Empty while the form is incomplete, which shows the placeholder instead. */
  sql: string
  placeholder: React.ReactNode
  className?: string
}) {
  return (
    <div className={cn("min-w-0 space-y-1.5", className)}>
      <div className="flex min-h-6 items-center justify-between gap-3">
        <p className="eyebrow">{label}</p>
        {sql && (
          <Button size="xs" variant="ghost" onClick={() => void copyText(sql, "Statement copied")}>
            <Copy />
            Copy
          </Button>
        )}
      </div>
      <Well className="max-h-44 text-hint leading-relaxed whitespace-pre-wrap">
        {sql || <span className="text-muted-foreground italic">{placeholder}</span>}
      </Well>
    </div>
  )
}

/** A short piece of guidance under a form, quieter than a `Notice`. */
export function FormNote({
  tone = "default",
  className,
  ...props
}: React.ComponentProps<"p"> & { tone?: Tone }) {
  return (
    <p
      className={cn(
        "text-hint leading-relaxed",
        tone === "default" && "text-muted-foreground",
        tone === "danger" && "text-destructive",
        tone === "warning" && "text-warning",
        tone === "success" && "text-success",
        className,
      )}
      {...props}
    />
  )
}
