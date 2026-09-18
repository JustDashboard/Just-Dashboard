import { cn } from "@/lib/utils"
import { VERSION } from "@/lib/version"

/**
 * The mark: a J drawn as a stencil — a slanted tittle over a stem that turns
 * into the tick of a checkmark at the foot. The two paths are the ones in
 * `public/LOGO.svg`, which is the file the mark ships as; this is the same
 * drawing inlined so it tints and stays crisp.
 *
 * It is `currentColor` rather than the pale blue it was drawn in, so the one
 * place the mark's colour is named is `--brand` in the palette and the glyph
 * tints with whatever it is placed inside. The two subpaths are separate rather
 * than one shape with a hole: the gap between tittle and stem is the background
 * showing through, not a counter, and it has to stay transparent over a card,
 * over the sidebar and over the sign-in wash alike.
 *
 * `label` is what the glyph stands in for in the accessible name, because it
 * stands in for a *word* rather than decorating one — "Just" beside the typeset
 * "Dashboard", the whole name where the mark appears alone. Without it the
 * sidebar's home link announces half the product's name.
 */
export function LogoGlyph({ label, className }: { label?: string; className?: string }) {
  return (
    <svg
      viewBox="0 0 522 648"
      fill="currentColor"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      focusable="false"
      className={className}
    >
      <path d="M516.476 155.307L516.97 155.34L517.252 155.667C517.049 156.97 517.562 167.496 517.514 170.102C517.242 192.526 517.214 214.952 517.432 237.377C517.663 256.937 517.391 277.333 517.331 296.958L517.34 375.675L517.546 416.672C517.663 427.92 517.948 440.181 517.242 451.385C514.297 455.584 510.753 456.422 507.055 459.324C502.715 462.728 498.32 465.544 493.913 468.737L430.362 514.885L332.242 586.139C325.386 591.075 255.09 642.485 253.047 642.485C249.786 640.343 246.593 636.995 243.757 634.35L227.898 619.634L169.155 564.943L109.434 509.145C101.508 501.621 91.2761 492.787 84.0074 485.006C75.0625 478.055 65.8737 468.145 57.6182 460.27C55.6235 458.365 52.4321 456.634 50.6614 454.78C38.4652 441.991 24.6113 430.914 12.2401 418.292C9.3116 415.302 7.0111 412.382 3.45068 410.138C5.04402 410.078 6.63337 409.841 8.22537 409.727C18.6994 408.987 29.2327 409.42 39.7231 409.401L111.687 409.474L155.226 409.354C162.546 409.338 170.084 409.256 177.403 409.468C179.227 409.578 181.062 410.116 182.464 411.344C187.373 415.634 192.287 420.001 197.279 424.196C211.78 436.331 226.122 448.651 240.301 461.162C246.014 466.114 251.177 471.195 257.16 475.834C261.414 479.131 268.576 486.598 272.883 489.174C274.312 489.044 280.759 483.146 282.502 481.82C292.339 474.325 302.124 467.031 311.613 459.077C311.293 449.927 311.727 439.033 311.821 429.756L311.85 385.56L311.822 328.803C311.693 309.843 311.289 289.811 311.996 270.98L351.566 248.079C355.106 246.071 358.956 244.477 362.379 242.284C366.615 239.567 371.101 237.396 375.457 234.918L431.84 202.998C433.551 202.025 435.44 200.574 437.31 199.49L465.748 183.357L507.903 159.785C510.763 158.159 513.398 157.13 516.476 155.307Z" />
      <path d="M516.34 2.44244L517.017 2.65999C517.223 4.12633 517.071 12.3071 517.16 14.4348C517.637 26.1424 517.292 37.9156 517.337 49.6116C517.47 69.2503 517.485 88.8899 517.375 108.529C517.33 112.3 517.675 123.919 517.207 126.731C513.793 129.192 504.682 134.116 500.848 136.253L473.16 151.946L367.839 211.857L327.737 234.33C321.808 237.713 318.714 238.686 312.76 242.792C312.477 242.709 312.094 242.667 312.079 242.429C311.704 236.691 311.667 224.28 311.683 218.863L311.796 152.235L311.658 131.426C311.624 126.828 311.435 121.316 311.971 116.843C314.317 115.463 317.309 113.176 319.768 112.189C325.608 109.846 330.1 106.805 335.421 103.738C342.125 99.8762 349.18 96.1752 355.963 92.415L394.958 70.6918C398.947 68.4767 402.737 66.0236 406.666 63.8223C414.816 59.2585 422.899 55.292 430.536 49.8568C432.77 48.2684 437.259 47.0816 439.764 45.5271C446.541 41.3236 453.643 37.6726 460.436 33.4786C462.35 32.1975 465.317 31.2965 467.158 30.0479C471.445 27.1391 476.228 24.9317 480.629 22.4137C488.152 18.1071 495.764 14.1893 503.236 9.81908C507.583 7.27666 512.278 5.41604 516.34 2.44244Z" />
    </svg>
  )
}

/**
 * The logo: the mark, then the word.
 *
 * "Just" used to be set in type beside "Dashboard" and coloured with the accent
 * — a wordmark standing in for a mark the product did not have. It has one now,
 * and the mark *is* the J, so setting the letter again next to it would say the
 * same thing twice. What is left reads as the name it always was: the glyph
 * carries "Just", the type carries "Dashboard".
 *
 * The version rides beside it because this is software an operator upgrades by
 * pulling and rebuilding: "which one am I looking at" is a real question, and
 * the answer belongs where they already look rather than on a page nobody
 * visits.
 */
export function Logo({
  size = "sm",
  version = true,
  className,
}: {
  size?: "sm" | "md" | "lg"
  /** Set false where the version would be noise — a splash, a narrow strip. */
  version?: boolean
  className?: string
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <LogoGlyph
        label="Just"
        className={cn(
          "w-auto shrink-0 text-brand",
          size === "sm" && "h-[1.15rem]",
          size === "md" && "h-[1.3rem]",
          size === "lg" && "h-[1.7rem]",
        )}
      />
      <span
        className={cn(
          "truncate leading-tight font-semibold tracking-tight",
          size === "sm" && "text-mark-sm",
          size === "md" && "text-mark-md",
          size === "lg" && "text-mark-lg",
        )}
      >
        Dashboard
      </span>
      {version && <LogoVersion />}
    </span>
  )
}

/**
 * The version, as small text beside the name and nothing else. No chip, no
 * border, no fill: a badge would give the number a frame the wordmark itself
 * does not have, which reads as the more important of the two. It is a
 * footnote to the name and should look like one. Tabular digits so it does
 * not shift when 0.5 becomes 0.10.
 */
export function LogoVersion({ className }: { className?: string }) {
  return (
    <span className={cn("numeric shrink-0 text-hint text-muted-foreground", className)}>
      {VERSION}
    </span>
  )
}

/**
 * What is left of the logo when there is no room for it — the collapsed sidebar
 * rail is three rem wide. It is the mark alone, which is the whole point of
 * having one: the rail now shows the same object the expanded sidebar does
 * rather than a letter standing in for it.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <LogoGlyph label="Just Dashboard" className={cn("h-[1.3rem] w-auto text-brand", className)} />
  )
}
