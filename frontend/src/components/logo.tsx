import { cn } from "@/lib/utils"
import { VERSION } from "@/lib/version"

/**
 * The mark: a J whose tittle is cut free of the stem and whose foot turns into
 * the tick of a checkmark. It is the drawing in `public/brand-logo/`, inlined so
 * it tints and stays crisp — those files are what ships outside the app (the
 * README, the browser icons), this is what the app draws.
 *
 * It is `currentColor` rather than the blue the files are filled with, so the
 * one place the mark's colour is named is `--brand` in the palette and the glyph
 * tints with whatever it is placed inside. The tittle is its own subpath rather
 * than a hole in one shape: the gap between it and the stem is the background
 * showing through, and it has to stay transparent over a card, the sidebar and
 * the sign-in wash alike.
 *
 * `small` draws `just-mark-small.svg`, the brand's cut for sixteen pixels and
 * under. In the regular drawing the gap between tittle and stem is a twentieth
 * of the height, which at that size is less than a pixel and antialiases shut;
 * the small cut opens it so the J still reads as two pieces.
 *
 * `label` is what the glyph stands in for in the accessible name, where it
 * stands in for the product rather than decorating something that already
 * names it.
 */
export function LogoGlyph({
  label,
  small,
  className,
}: {
  label?: string
  small?: boolean
  className?: string
}) {
  return (
    <svg
      viewBox={small ? "38.1 12 179.8 232" : "0 0 198.4 256"}
      fill="currentColor"
      role={label ? "img" : undefined}
      aria-label={label}
      aria-hidden={label ? undefined : true}
      focusable="false"
      className={className}
    >
      <path d={small ? MARK_SMALL : MARK} />
    </svg>
  )
}

/**
 * The logo: the brand's horizontal lockup, then the version.
 *
 * "Just Dashboard" is the brand's own lettering, outlined, not type set in the
 * interface face beside the glyph. The lockup fixes how large the mark is
 * against the words and how far apart they sit; rebuilding it from the glyph
 * and Source Sans would be a second, unofficial logo that drifts from the
 * brand's files. It is one colour, `text-brand`, the way
 * `just-dashboard-on-dark.svg` draws it on a ground this dark.
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
  size?: "sm" | "md"
  /** Set false where the version would be noise — a narrow strip. */
  version?: boolean
  className?: string
}) {
  return (
    <span className={cn("flex min-w-0 items-center gap-2", className)}>
      <svg
        viewBox="0 0 1131 256"
        fill="currentColor"
        role="img"
        aria-label="Just Dashboard"
        focusable="false"
        className={cn("w-auto shrink-0 text-brand", size === "sm" ? "h-7" : "h-8")}
      >
        <path d={MARK} />
        <path d={WORDMARK} />
      </svg>
      {/* The mark rises above the words, so the lettering's centre line is
          below the lockup's: centred on the box, the version rode high, like
          a superscript. Two pixels is that difference at both sizes. */}
      {version && <LogoVersion className="translate-y-0.5" />}
    </span>
  )
}

/**
 * The stacked lockup, mark over name, for a composition centred on the logo —
 * the start-up splash — where the horizontal one sits off-balance. The same two
 * drawings, placed where `just-dashboard-stacked.svg` places them.
 */
export function LogoStacked({ className }: { className?: string }) {
  return (
    <svg
      viewBox="0 0 884 464"
      fill="currentColor"
      role="img"
      aria-label="Just Dashboard"
      focusable="false"
      className={cn("w-auto shrink-0 text-brand", className)}
    >
      <path d={MARK} transform="translate(342.98 0)" />
      <path d={WORDMARK} transform="translate(-246.4 212.08)" />
    </svg>
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
 * rail is three rem wide. It is the mark alone, the same object the expanded
 * sidebar leads with.
 */
export function LogoMark({ className }: { className?: string }) {
  return (
    <LogoGlyph label="Just Dashboard" className={cn("h-[1.3rem] w-auto text-brand", className)} />
  )
}

// The paths of `public/brand-logo/just-dashboard-horizontal.svg` and
// `just-mark-small.svg`, rounded to hundredths of a unit — a thousandth of a
// pixel at the largest size the app draws them.
const MARK =
  "M198.4 0L198.4 49.6L116.8 96.71L116.8 47.11ZM198.4 62.4L198.4 195.49L93.6 256L0 162.4L70.14 162.4L101.76 194.02L116.8 185.33L116.8 109.51Z"
const MARK_SMALL =
  "M217.9 12L217.9 56.95L143.95 99.65L143.95 54.7ZM217.9 77.25L217.9 189.16L122.93 244L38.1 159.17L101.67 159.17L130.32 187.83L143.95 179.96L143.95 119.95Z"
const WORDMARK =
  "M248.96 165.67V160.17H264.74V165.67Q264.74 171.08 267.9 174.79Q271.07 178.5 277.88 178.5Q284.75 178.5 287.83 174.84Q290.92 171.18 290.92 165.14V98.51H307.28V166.03Q307.28 174.17 303.67 180.42Q300.07 186.67 293.5 190.19Q286.92 193.71 278.02 193.71Q268.49 193.71 261.98 190.08Q255.48 186.46 252.22 180.11Q248.96 173.75 248.96 165.67ZM365.49 128.85H381.06V192.08H366.64L365.4 183.64Q362.61 188.15 356.98 190.93Q351.35 193.71 345.01 193.71Q334.09 193.71 327.97 186.84Q321.84 179.97 321.84 168.35V128.81H337.45V162.87Q337.45 171.87 340.92 175.84Q344.38 179.82 350.9 179.82Q358.3 179.82 361.89 175.41Q365.49 171.01 365.49 161.97ZM389.8 172.93H404.61Q404.79 177.03 407.74 179.41Q410.69 181.79 415.94 181.79Q421.5 181.79 424.37 179.73Q427.24 177.68 427.24 174.29Q427.24 171.96 425.75 170.2Q424.26 168.45 419.94 167.45L408.59 164.85Q400.06 162.9 395.87 158.63Q391.67 154.36 391.67 146.76Q391.67 140.56 394.92 136.14Q398.17 131.72 403.82 129.33Q409.47 126.95 416.73 126.95Q423.85 126.95 429.22 129.49Q434.59 132.02 437.61 136.62Q440.64 141.21 440.68 147.47H425.87Q425.79 143.34 423.24 140.97Q420.68 138.6 416.1 138.6Q411.55 138.6 408.99 140.62Q406.43 142.65 406.43 146.13Q406.43 151.41 414.57 153.33L425.97 155.93Q433.95 157.71 437.97 161.72Q442 165.74 442 173.03Q442 179.36 438.64 184.04Q435.29 188.73 429.31 191.22Q423.33 193.71 415.48 193.71Q403.69 193.71 396.74 187.97Q389.8 182.24 389.8 172.93ZM445.02 128.85H485.06V141.85H445.02ZM472.82 192.08H457.25V109.12H472.82ZM557.48 192.08H526.88V99.51H556.92Q570.8 99.51 581.21 105.35Q591.63 111.18 597.47 121.61Q603.3 132.04 603.3 145.91Q603.3 159.59 597.55 170Q591.8 180.41 581.5 186.24Q571.19 192.08 557.48 192.08ZM539.45 105.4V186.3L533.53 180.48H556.58Q566.92 180.48 574.43 176.25Q581.94 172.03 586.05 164.25Q590.16 156.48 590.16 145.91Q590.16 135.16 586 127.35Q581.84 119.54 574.22 115.33Q566.6 111.11 556.02 111.11H533.53ZM632.92 193.67Q622.95 193.67 617.29 188.43Q611.62 183.18 611.62 174.7Q611.62 166.08 617.74 160.9Q623.85 155.73 634.94 154.83L654.65 153.28V151.54Q654.65 146.26 652.73 143.15Q650.8 140.03 647.47 138.69Q644.13 137.35 639.83 137.35Q632.27 137.35 628.14 140.48Q624.02 143.61 624.02 149.32H613.51Q613.51 142.74 616.83 137.91Q620.15 133.07 626.25 130.41Q632.34 127.75 640.35 127.75Q648.19 127.75 654.01 130.47Q659.83 133.19 663.11 138.77Q666.4 144.34 666.4 152.84V192.08H656.09L654.88 181.91Q652.41 187.34 646.46 190.51Q640.52 193.67 632.92 193.67ZM636.39 184.31Q644.96 184.31 649.84 179.09Q654.71 173.86 654.71 165.13V161.75L638.78 163.08Q630.73 163.73 627.24 166.73Q623.76 169.73 623.76 174.23Q623.76 179.26 627.15 181.79Q630.55 184.31 636.39 184.31ZM675.35 173.86H686.8Q686.91 178.62 690.31 181.4Q693.71 184.18 699.62 184.18Q706.02 184.18 709.49 181.68Q712.95 179.18 712.95 174.95Q712.95 171.83 711.11 169.77Q709.27 167.72 704.33 166.52L693.34 163.89Q685.08 161.9 681.02 157.65Q676.95 153.39 676.95 146.52Q676.95 140.73 679.97 136.52Q682.99 132.31 688.32 130.03Q693.66 127.75 700.55 127.75Q707.4 127.75 712.49 130.15Q717.57 132.55 720.45 136.92Q723.33 141.28 723.4 147.31H711.95Q711.81 142.47 708.71 139.77Q705.6 137.07 700.15 137.07Q694.53 137.07 691.47 139.46Q688.41 141.85 688.41 146.13Q688.41 152.45 697.62 154.7L708.59 157.33Q716.6 159.21 720.51 163.21Q724.42 167.2 724.42 174.17Q724.42 180.09 721.26 184.51Q718.11 188.93 712.5 191.3Q706.89 193.67 699.43 193.67Q688.4 193.67 681.88 188.24Q675.35 182.8 675.35 173.86ZM747.21 192.01H735.24V97.86H747.27V139.11Q750.21 133.94 755.68 130.84Q761.15 127.75 768.5 127.75Q780.47 127.75 786.3 134.76Q792.14 141.78 792.14 153.75V192.08H780.17V156.48Q780.17 150.09 778.29 146.21Q776.42 142.33 773.06 140.49Q769.7 138.65 765.25 138.65Q759.47 138.65 755.44 141.22Q751.42 143.78 749.31 148.16Q747.21 152.54 747.21 157.94ZM817.1 192.08H806.28V97.86H818.25V140.23Q821.41 134.09 827.42 130.89Q833.42 127.7 841.05 127.7Q850.08 127.7 856.49 132.01Q862.9 136.33 866.34 143.85Q869.79 151.37 869.79 161.08Q869.79 170.5 866.2 177.88Q862.62 185.27 856.04 189.47Q849.46 193.67 840.37 193.67Q833 193.67 827.06 190.35Q821.11 187.02 818.05 180.73ZM818.38 160.63Q818.38 167.08 820.76 172.09Q823.14 177.1 827.59 179.91Q832.04 182.72 838.15 182.72Q844.32 182.72 848.68 179.88Q853.04 177.05 855.37 172.07Q857.69 167.08 857.69 160.63Q857.69 154.17 855.37 149.23Q853.04 144.28 848.68 141.41Q844.32 138.54 838.15 138.54Q832.04 138.54 827.59 141.38Q823.14 144.22 820.76 149.17Q818.38 154.12 818.38 160.63ZM876.42 160.74Q876.42 151.06 880.64 143.66Q884.86 136.26 892.25 132.03Q899.65 127.81 909.03 127.81Q918.48 127.81 925.8 132.03Q933.13 136.26 937.35 143.66Q941.57 151.06 941.57 160.74Q941.57 170.35 937.35 177.76Q933.13 185.17 925.8 189.39Q918.48 193.62 909.03 193.62Q899.65 193.62 892.25 189.39Q884.86 185.17 880.64 177.76Q876.42 170.35 876.42 160.74ZM888.52 160.68Q888.52 167.2 891.15 172.21Q893.78 177.22 898.38 180.02Q902.98 182.83 909.03 182.83Q915.07 182.83 919.67 180.02Q924.27 177.22 926.87 172.21Q929.47 167.2 929.47 160.68Q929.47 154.1 926.87 149.15Q924.27 144.21 919.67 141.4Q915.07 138.59 909.03 138.59Q902.98 138.59 898.38 141.4Q893.78 144.21 891.15 149.15Q888.52 154.1 888.52 160.68ZM970.58 193.67Q960.61 193.67 954.95 188.43Q949.28 183.18 949.28 174.7Q949.28 166.08 955.4 160.9Q961.52 155.73 972.61 154.83L992.32 153.28V151.54Q992.32 146.26 990.39 143.15Q988.47 140.03 985.13 138.69Q981.8 137.35 977.5 137.35Q969.93 137.35 965.81 140.48Q961.68 143.61 961.68 149.32H951.18Q951.18 142.74 954.5 137.91Q957.81 133.07 963.91 130.41Q970 127.75 978.02 127.75Q985.86 127.75 991.67 130.47Q997.49 133.19 1000.78 138.77Q1004.06 144.34 1004.06 152.84V192.08H993.75L992.54 181.91Q990.07 187.34 984.13 190.51Q978.18 193.67 970.58 193.67ZM974.05 184.31Q982.63 184.31 987.5 179.09Q992.37 173.86 992.37 165.13V161.75L976.44 163.08Q968.39 163.73 964.91 166.73Q961.42 169.73 961.42 174.23Q961.42 179.26 964.82 181.79Q968.21 184.31 974.05 184.31ZM1053.91 129.01V140.05H1048.57Q1040.05 140.05 1035.07 144.97Q1030.09 149.89 1030.09 158.93V192.08H1018.12V129.57H1029.37L1030.41 142.19H1029.26Q1030.54 136.01 1035.37 132.13Q1040.2 128.26 1047.58 128.26Q1049.15 128.26 1050.65 128.42Q1052.15 128.57 1053.91 129.01ZM1087.75 193.67Q1078.68 193.67 1072.15 189.47Q1065.62 185.27 1062.09 177.88Q1058.57 170.5 1058.57 160.97Q1058.57 151.37 1062.12 143.88Q1065.67 136.38 1072.35 132.07Q1079.02 127.75 1088.32 127.75Q1095.55 127.75 1101.27 130.75Q1107 133.75 1110.1 139.56V97.86H1122.07V192.08H1111.25L1110.3 180.73Q1107.24 187.02 1101.24 190.35Q1095.24 193.67 1087.75 193.67ZM1090.2 182.72Q1096.38 182.72 1100.8 179.91Q1105.21 177.1 1107.59 172.09Q1109.97 167.08 1109.97 160.63Q1109.97 154.12 1107.59 149.17Q1105.21 144.22 1100.8 141.38Q1096.38 138.54 1090.2 138.54Q1084.08 138.54 1079.7 141.41Q1075.31 144.28 1072.99 149.23Q1070.66 154.17 1070.66 160.63Q1070.66 167.08 1072.99 172.07Q1075.31 177.05 1079.7 179.88Q1084.08 182.72 1090.2 182.72Z"
