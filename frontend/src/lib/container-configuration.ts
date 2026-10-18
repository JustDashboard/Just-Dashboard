import type { ContainerSpec } from "./types"

export function changedSpecFields(before: ContainerSpec, after: ContainerSpec): string[] {
  return [...new Set([...Object.keys(before), ...Object.keys(after)])].filter(
    (key) =>
      JSON.stringify(before[key as keyof ContainerSpec]) !==
      JSON.stringify(after[key as keyof ContainerSpec]),
  )
}
