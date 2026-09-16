import { defineConfig, globalIgnores } from "eslint/config"
import nextVitals from "eslint-config-next/core-web-vitals"
import nextTs from "eslint-config-next/typescript"

import designSystem from "./eslint-rules/design-system.mjs"

const eslintConfig = defineConfig([
  ...nextVitals,
  ...nextTs,
  {
    plugins: { "design-system": designSystem },
    rules: {
      "design-system/tokens": "error",
      // Reaching past a primitive to the thing it wraps is the other mechanical
      // failure. Each of these has exactly one legitimate assembler, listed in
      // the override below.
      "no-restricted-imports": [
        "error",
        {
          paths: [
            {
              name: "@/components/ui/dialog",
              message:
                "Raw Dialog is assembled only by components/modal.tsx. A page opens a Modal or a PaletteModal.",
            },
            {
              name: "@/components/ui/sheet",
              message:
                "Raw Sheet is assembled only by components/side-panel.tsx. A page opens a SidePanel.",
            },
            {
              name: "@heroicons/react/24/solid",
              message: "Icons come from components/icons.tsx, which defines the vocabulary.",
            },
            {
              name: "@heroicons/react/24/outline",
              message: "Icons come from components/icons.tsx.",
            },
            {
              name: "sonner",
              message: "Toasts go through lib/toast.ts, which owns what a failure says.",
            },
          ],
        },
      ],
    },
  },
  {
    // The components that legitimately assemble what everything else composes.
    files: [
      "src/components/modal.tsx",
      "src/components/side-panel.tsx",
      "src/components/icons.tsx",
      "src/lib/toast.ts",
      "src/components/ui/sonner.tsx",
      // Generated shadcn primitives that wrap Dialog/Sheet themselves.
      "src/components/ui/command.tsx",
      "src/components/ui/sidebar.tsx",
    ],
    rules: { "no-restricted-imports": "off" },
  },
  {
    // Where a value is *defined* rather than consumed. The generated shadcn
    // primitives own the control faces; `logo.tsx` owns the wordmark's own ramp;
    // `components/metrics/` owns the chart palette; `icon-action.tsx` owns the
    // reveal rule itself.
    files: [
      "src/components/ui/**",
      "src/components/icon-action.tsx",
      "src/components/logo.tsx",
      "src/components/metrics/**",
      "src/components/logs/histogram.tsx",
      "src/components/git/graph-panel.tsx",
      "src/components/database/diagram/**",
      "src/components/xterm-pane.tsx",
    ],
    rules: { "design-system/tokens": "off" },
  },
  globalIgnores([
    // Default ignores of eslint-config-next:
    ".next/**",
    "out/**",
    "build/**",
    "next-env.d.ts",
    // Browser reports bundle third-party trace viewers, not application source.
    "playwright-report/**",
    "test-results/**",
    // The Monaco runtime, copied in from node_modules by
    // scripts/sync-monaco.mjs. It is a vendored build, not source: linting it
    // buries every real finding under twenty-five thousand from minified code
    // nobody here wrote.
    "public/monaco/**",
  ]),
])

export default eslintConfig
