"use client"

import { StepArrows as Arrows, useDetailTab, type Step } from "@/components/detail-page"
import { ViewTabList, type TabView } from "@/components/tabs"

/**
 * What an account's page and a group's page shared before every detail page
 * moved onto one frame (`components/detail-page.tsx`). Neither page imports
 * it now; the names stay, as thin wrappers over the shared ones, for anything
 * that still does until WP-39 removes them.
 */

export type { Step }

/** @deprecated use `TabView` from `@/components/tabs`. */
export type View<T extends string> = TabView<T>

/** @deprecated use `useDetailTab` from `@/components/detail-page`. */
export function useAddressTab<T extends string>(tabs: readonly T[]): [T, (next: string) => void] {
  const [tab, setTab] = useDetailTab(tabs)
  return [tab, setTab]
}

/** @deprecated use `ViewTabList` from `@/components/tabs`, inside the page's `Tabs`. */
export function ViewTabs<T extends string>({
  label,
  views,
  shown,
}: {
  label: string
  views: View<T>[]
  shown: T
}) {
  return <ViewTabList label={label} views={views} value={shown} />
}

/** @deprecated use `StepArrows` from `@/components/detail-page`, whose prop is `what`. */
export function StepArrows({ noun, previous, next }: { noun: string; previous: Step; next: Step }) {
  return <Arrows what={noun} previous={previous} next={next} />
}
