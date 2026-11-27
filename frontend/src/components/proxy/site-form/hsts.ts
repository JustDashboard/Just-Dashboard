/*
 * Kept out of the site form so `deploy/settings/route-access.ts`, a model module, can read the
 * choices without importing a component file.
 */

/**
 * How long browsers keep the HSTS policy. Six months is what the form always
 * wrote. Shared with a deployment's Domains settings, whose HSTS control
 * offers the same choices for the same header.
 */
export const HSTS_MAX_AGES: { value: number; label: string }[] = [
  { value: 300, label: "5 minutes (while testing)" },
  { value: 86400, label: "1 day" },
  { value: 2592000, label: "30 days" },
  { value: 15552000, label: "6 months" },
  { value: 31536000, label: "1 year" },
  { value: 63072000, label: "2 years" },
]
export const HSTS_DEFAULT_MAX_AGE = 15552000
export const HSTS_PRELOAD_MAX_AGE = 31536000
