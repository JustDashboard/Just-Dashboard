"use client"

import { cn } from "@/lib/utils"
import type { DeploymentVariable } from "@/lib/types"
import { ProductLogo } from "@/components/product-logo"
import { ToggleGroup, ToggleGroupItem } from "@/components/ui/toggle-group"
import { Code, LockClosed } from "@/components/icons"

/**
 * A variable's two faces, shared by an environment's variables and the
 * shared ones they read, so the two lists draw and choose a type alike.
 */

type Sensitivity = DeploymentVariable["sensitivity"]

/**
 * The tile a variable is drawn on: the service its name says holds it, the
 * engine a reference points at, or — for a name that says nothing — a lock
 * for a secret and brackets for a setting. Never a guess: `DATABASE_URL`
 * without a reference is no product.
 */
export function VariableMark({
  variable,
  product,
}: {
  variable: Pick<DeploymentVariable, "sensitivity">
  product?: string
}) {
  return (
    <ProductLogo
      size="sm"
      id={product}
      fallback={variable.sensitivity === "secret" ? LockClosed : Code}
    />
  )
}

/**
 * A stored value read back, drawn in the place its mask was, so revealing one
 * moves nothing on the page. It was a framed band under the row, which pushed
 * every variable below it down for the length of a look. One line however long
 * the value is: it scrolls sideways, a press selects all of it, and the row's
 * copy verb takes it exactly, line breaks included. A press stays here because
 * the row around it opens the editor.
 */
export function RevealedValue({ name, value }: { name: string; value: string }) {
  return (
    <span
      tabIndex={0}
      aria-label={`Value of ${name}`}
      onClick={(event) => event.stopPropagation()}
      className={cn(
        "block h-4 animate-rise cursor-text [scrollbar-width:none] overflow-x-auto rounded-sm font-mono leading-4 whitespace-nowrap focus-ring-inset select-all [&::-webkit-scrollbar]:hidden",
        value ? "text-foreground" : "text-muted-foreground",
      )}
    >
      {value || "empty"}
    </span>
  )
}

/**
 * Secret or setting, as the two faces of one control. It was two filter chips
 * — a control for narrowing a list, pressed into service as a choice — beside
 * a field three times their height.
 */
export function ValueType({
  value,
  onChange,
}: {
  value: Sensitivity
  onChange: (value: Sensitivity) => void
}) {
  return (
    <ToggleGroup
      type="single"
      variant="outline"
      aria-label="Value type"
      value={value}
      onValueChange={(next) => next && onChange(next as Sensitivity)}
      className="w-full sm:w-auto"
    >
      <ToggleGroupItem value="secret" className="h-11 flex-1 gap-1.5 text-body sm:h-9 sm:flex-none">
        <LockClosed className={cn("size-3.5", value === "secret" && "text-brand")} /> Secret
      </ToggleGroupItem>
      <ToggleGroupItem value="plain" className="h-11 flex-1 gap-1.5 text-body sm:h-9 sm:flex-none">
        <Code className={cn("size-3.5", value === "plain" && "text-brand")} /> Config
      </ToggleGroupItem>
    </ToggleGroup>
  )
}
