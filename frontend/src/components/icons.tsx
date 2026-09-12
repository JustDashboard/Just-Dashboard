import { forwardRef } from "react"
import type { ForwardRefExoticComponent, Ref, RefAttributes, SVGProps } from "react"
import {
  AdjustmentsHorizontalIcon,
  ArchiveBoxArrowDownIcon,
  ArchiveBoxIcon,
  ArrowDownIcon,
  ArrowDownTrayIcon,
  ArrowLeftIcon,
  ArrowLeftStartOnRectangleIcon,
  ArrowPathIcon,
  ArrowRightEndOnRectangleIcon,
  ArrowRightIcon,
  ArrowTopRightOnSquareIcon,
  ArrowTrendingUpIcon,
  ArrowTurnDownLeftIcon,
  ArrowTurnUpLeftIcon,
  ArrowTurnUpRightIcon,
  ArrowUpCircleIcon,
  ArrowUpIcon,
  ArrowUpRightIcon,
  ArrowUturnDownIcon,
  ArrowUturnLeftIcon,
  ArrowUturnRightIcon,
  ArrowsPointingInIcon,
  ArrowsPointingOutIcon,
  ArrowsRightLeftIcon,
  ArrowsUpDownIcon,
  BackspaceIcon,
  Bars3BottomLeftIcon,
  Bars3Icon,
  BellIcon,
  BoldIcon,
  BookmarkIcon,
  BoltIcon,
  BugAntIcon,
  CalculatorIcon,
  ChartBarIcon,
  ChatBubbleLeftRightIcon,
  CheckCircleIcon,
  CheckIcon,
  ChevronDoubleDownIcon,
  ChevronDoubleUpIcon,
  ChevronDownIcon,
  ChevronLeftIcon,
  ChevronRightIcon,
  ChevronUpIcon,
  CircleStackIcon,
  ClipboardDocumentListIcon,
  ClipboardIcon,
  ClockIcon,
  CloudArrowDownIcon,
  CloudArrowUpIcon,
  CodeBracketIcon,
  CodeBracketSquareIcon,
  Cog6ToothIcon,
  CommandLineIcon,
  ComputerDesktopIcon,
  CpuChipIcon,
  CubeIcon,
  DocumentArrowDownIcon,
  DocumentDuplicateIcon,
  DocumentIcon,
  DocumentMagnifyingGlassIcon,
  DocumentPlusIcon,
  DocumentTextIcon,
  EllipsisHorizontalIcon,
  ExclamationCircleIcon,
  ExclamationTriangleIcon,
  EyeIcon,
  EyeSlashIcon,
  FilmIcon,
  FingerPrintIcon,
  FolderIcon,
  FolderMinusIcon,
  FolderOpenIcon,
  FolderPlusIcon,
  FunnelIcon,
  GifIcon,
  GlobeAltIcon,
  HashtagIcon,
  HeartIcon,
  HomeIcon,
  InboxIcon,
  InformationCircleIcon,
  KeyIcon,
  LifebuoyIcon,
  LinkIcon,
  ListBulletIcon,
  LockClosedIcon,
  LockOpenIcon,
  MagnifyingGlassCircleIcon,
  MagnifyingGlassIcon,
  MagnifyingGlassMinusIcon,
  MapIcon,
  MapPinIcon,
  MinusIcon,
  MinusSmallIcon,
  MusicalNoteIcon,
  NoSymbolIcon,
  NumberedListIcon,
  PaintBrushIcon,
  PauseIcon,
  PencilSquareIcon,
  PhotoIcon,
  PlayIcon,
  PlusIcon,
  PresentationChartLineIcon,
  PuzzlePieceIcon,
  QuestionMarkCircleIcon,
  QueueListIcon,
  RadioIcon,
  RectangleGroupIcon,
  RectangleStackIcon,
  RssIcon,
  ScissorsIcon,
  ServerStackIcon,
  ShareIcon,
  ShieldCheckIcon,
  ShieldExclamationIcon,
  SignalIcon,
  SlashIcon,
  SparklesIcon,
  Square2StackIcon,
  Square3Stack3DIcon,
  Squares2X2Icon,
  SquaresPlusIcon,
  StarIcon,
  StopCircleIcon,
  SunIcon,
  SwatchIcon,
  TableCellsIcon,
  TrashIcon,
  UnderlineIcon,
  UserCircleIcon,
  UserMinusIcon,
  UserPlusIcon,
  UsersIcon,
  ViewColumnsIcon,
  ViewfinderCircleIcon,
  WifiIcon,
  WindowIcon,
  WrenchIcon,
  XCircleIcon,
  XMarkIcon,
  BookOpenIcon,
  MoonIcon,
} from "@heroicons/react/24/solid"

/**
 * The icon vocabulary.
 *
 * Every glyph in the product comes from here, and here is the only file that
 * names the icon library. That is the same argument `logo.tsx` makes about the
 * wordmark: an icon set is a visual language, and one imported directly by a
 * hundred and forty feature files is one nobody can change, audit or keep
 * consistent. A page that wants "the delete glyph" should not be choosing
 * between `Trash`, `Trash2` and `Delete` — it asks for `Trash` and gets
 * whatever this file has decided that means.
 *
 * The set is **Heroicons** (the 24px solid family), and every name below is
 * this product's own word for the glyph — `Trash`, not `TrashIcon` — so the
 * hundred-plus files importing this module never name Heroicons themselves.
 * Where Heroicons draws one thing for several of our words — `Clock` and
 * `Stopwatch`, `Link` and `Linked`, `Shield` and `ShieldCheck` — they share
 * the glyph, because that is what they always were. Where Heroicons has no
 * drawing at all — there is no floppy disk, no git branch, no sidebar — the
 * mapping picks the nearest true thing (`DocumentArrowDown` for save,
 * `Share` for a branch, `ViewColumns` for a sidebar) and says so next to it.
 *
 * **Both themes come free, and that is a property to protect.** Every glyph
 * here paints with `fill="currentColor"` and nothing else — no hard-coded hex,
 * no gradient, no light-mode assumption. An icon is therefore whatever the text
 * colour around it is, which means it inherits `text-muted-foreground` on a
 * near-white card and on a near-black one without either being a special case.
 *
 * File-type glyphs do not live here. The file browser's per-extension
 * vocabulary is Material Design Icons in `files/file-icon.tsx` — a general UI
 * set has no `JSON` or `JPG` to draw, and MDI's file boxes do. The `File`,
 * `Folder*` and `Acronym*` names below are only the generic chrome (a new
 * folder button, an empty-state sheet, a JSON badge outside the file browser).
 */

export type IconProps = SVGProps<SVGSVGElement> & {
  size?: string | number
  title?: string
}

/** What every icon in this file is, for anything that takes one as a value. */
export type Icon = ForwardRefExoticComponent<Omit<IconProps, "ref"> & RefAttributes<SVGSVGElement>>

export type { Icon as IconComponent }

type HeroIcon = ForwardRefExoticComponent<
  Omit<SVGProps<SVGSVGElement>, "ref"> & {
    title?: string
    titleId?: string
  } & RefAttributes<SVGSVGElement>
>

/**
 * One product icon out of one Heroicon.
 *
 * Heroicons size themselves purely from the class around them, so a bare
 * `<Pin />` with no `size-*` class would render at the SVG default of
 * 300×150. The adapter defaults an unsized glyph to 16, the size the old set
 * was drawn at and the size most rows render at; any `size-*` class still wins,
 * because CSS beats the width/height attributes below.
 */
function adapt(Hero: HeroIcon, name: string): Icon {
  function Adapted({ size = 16, title, ...props }: IconProps, ref: Ref<SVGSVGElement>) {
    return <Hero ref={ref} width={size} height={size} title={title} {...props} />
  }
  const Forwarded = forwardRef<SVGSVGElement, Omit<IconProps, "ref">>(Adapted)
  Forwarded.displayName = name
  return Forwarded
}

export const Check: Icon = adapt(CheckIcon, "Check")

/* Arrows, chevrons and movement. `ChevronDouble*` is the "jump to the end"
   family; the single chevrons are navigation and disclosure. `ArrowMove` is a
   drag handle — Heroicons draws expand, not move, and outward arrows are the
   nearest true thing. */
export const ArrowDown: Icon = adapt(ArrowDownIcon, "ArrowDown")
export const ArrowLeft: Icon = adapt(ArrowLeftIcon, "ArrowLeft")
export const ArrowRight: Icon = adapt(ArrowRightIcon, "ArrowRight")
export const ArrowUp: Icon = adapt(ArrowUpIcon, "ArrowUp")
export const ArrowCircleUp: Icon = adapt(ArrowUpCircleIcon, "ArrowCircleUp")
export const ArrowUpRight: Icon = adapt(ArrowUpRightIcon, "ArrowUpRight")
export const ArrowLeftRight: Icon = adapt(ArrowsRightLeftIcon, "ArrowLeftRight")
export const ArrowUpDown: Icon = adapt(ArrowsUpDownIcon, "ArrowUpDown")
export const ArrowMove: Icon = adapt(ArrowsPointingOutIcon, "ArrowMove")
export const ChevronDown: Icon = adapt(ChevronDownIcon, "ChevronDown")
export const ChevronLeft: Icon = adapt(ChevronLeftIcon, "ChevronLeft")
export const ChevronRight: Icon = adapt(ChevronRightIcon, "ChevronRight")
export const ChevronUp: Icon = adapt(ChevronUpIcon, "ChevronUp")
export const ChevronDoubleDown: Icon = adapt(ChevronDoubleDownIcon, "ChevronDoubleDown")
export const ChevronDoubleUp: Icon = adapt(ChevronDoubleUpIcon, "ChevronDoubleUp")
export const CornerDownLeft: Icon = adapt(ArrowTurnDownLeftIcon, "CornerDownLeft")
export const CornerUpLeft: Icon = adapt(ArrowTurnUpLeftIcon, "CornerUpLeft")
export const CornerUpRight: Icon = adapt(ArrowTurnUpRightIcon, "CornerUpRight")
export const External: Icon = adapt(ArrowTopRightOnSquareIcon, "External")
export const Fullscreen: Icon = adapt(ArrowsPointingOutIcon, "Fullscreen")
export const FullscreenClose: Icon = adapt(ArrowsPointingInIcon, "FullscreenClose")

/* The verbs. Heroicons has no floppy disk — `DocumentArrowDown` (a sheet being
   kept) is the save glyph. `Pencil` is the squared edit pencil; `Filter` is
   the funnel; refresh and the spinner below are the same circular arrow. */
export const Plus: Icon = adapt(PlusIcon, "Plus")
export const Minus: Icon = adapt(MinusIcon, "Minus")
export const Cross: Icon = adapt(XMarkIcon, "Cross")
export const Copy: Icon = adapt(DocumentDuplicateIcon, "Copy")
export const Clipboard: Icon = adapt(ClipboardIcon, "Clipboard")
export const Trash: Icon = adapt(TrashIcon, "Trash")
export const Pencil: Icon = adapt(PencilSquareIcon, "Pencil")
export const FloppyDisk: Icon = adapt(DocumentArrowDownIcon, "FloppyDisk")
export const Download: Icon = adapt(ArrowDownTrayIcon, "Download")
export const CloudDownload: Icon = adapt(CloudArrowDownIcon, "CloudDownload")
export const CloudUpload: Icon = adapt(CloudArrowUpIcon, "CloudUpload")
export const MagnifyingGlass: Icon = adapt(MagnifyingGlassIcon, "MagnifyingGlass")
export const MagnifyingGlassMinus: Icon = adapt(MagnifyingGlassMinusIcon, "MagnifyingGlassMinus")
export const Filter: Icon = adapt(FunnelIcon, "Filter")
export const RefreshClockwise: Icon = adapt(ArrowPathIcon, "RefreshClockwise")
export const RotateClockwise: Icon = adapt(ArrowUturnRightIcon, "RotateClockwise")
export const RotateCounterClockwise: Icon = adapt(ArrowUturnLeftIcon, "RotateCounterClockwise")
export const Backspace: Icon = adapt(BackspaceIcon, "Backspace")
export const Crop: Icon = adapt(ScissorsIcon, "Crop")
export const Sparkles: Icon = adapt(SparklesIcon, "Sparkles")
export const Wrench: Icon = adapt(WrenchIcon, "Wrench")
export const Calculator: Icon = adapt(CalculatorIcon, "Calculator")
export const Fingerprint: Icon = adapt(FingerPrintIcon, "Fingerprint")
export const Hash: Icon = adapt(HashtagIcon, "Hash")
export const Command: Icon = adapt(CommandLineIcon, "Command")
export const Play: Icon = adapt(PlayIcon, "Play")
export const Pause: Icon = adapt(PauseIcon, "Pause")
export const StopCircle: Icon = adapt(StopCircleIcon, "StopCircle")
export const PlusSquareSmall: Icon = adapt(DocumentPlusIcon, "PlusSquareSmall")

/* Text and matching affordances — the search bar's case/word/regex toggles and
   the editor's wrap switch. `SlashForward` is the regex toggle because `/…/` is
   how a regular expression is written. The case toggle is a bold B, the
   whole-word toggle an underlined word, the wrap toggle a line turning back on
   itself. */
export const TextFormat: Icon = adapt(Bars3BottomLeftIcon, "TextFormat")
export const TextTitle: Icon = adapt(UnderlineIcon, "TextTitle")
export const TextUppercase: Icon = adapt(BoldIcon, "TextUppercase")
export const SlashForward: Icon = adapt(SlashIcon, "SlashForward")
export const CodeWrap: Icon = adapt(ArrowUturnDownIcon, "CodeWrap")
export const CodeBracket: Icon = adapt(CodeBracketIcon, "CodeBracket")
export const Code: Icon = adapt(CodeBracketSquareIcon, "Code")

/* State and feedback.
 *
 * `Stop` is the alert-in-a-circle — the exclamation in a ring, not the media
 * control, and it is the app's warning glyph. The media stop is `StopCircle`,
 * the square in a ring. Getting these two the wrong way round puts a warning
 * triangle on the button that stops a container.
 *
 * `DotMark` is the radio/dot indicator, always rendered tiny with a fill — the
 * small dash reads as a dot at that size. It is not `Status` in
 * `components/status-dot.tsx`, which is the dot *and* its label; the two names
 * collided in every file that wanted both. `Slash` is prohibition, the
 * circle-with-a-line, for "not available on this host". */
export const LoaderCircle: Icon = adapt(ArrowPathIcon, "LoaderCircle")
export const CheckCircle: Icon = adapt(CheckCircleIcon, "CheckCircle")
export const CrossCircle: Icon = adapt(XCircleIcon, "CrossCircle")
export const DotMark: Icon = adapt(MinusSmallIcon, "DotMark")
export const Slash: Icon = adapt(NoSymbolIcon, "Slash")
export const Question: Icon = adapt(QuestionMarkCircleIcon, "Question")
export const Information: Icon = adapt(InformationCircleIcon, "Information")
export const Stop: Icon = adapt(ExclamationCircleIcon, "Stop")
export const Warning: Icon = adapt(ExclamationTriangleIcon, "Warning")
export const WarningFill: Icon = adapt(ExclamationTriangleIcon, "WarningFill")
export const Stopwatch: Icon = adapt(ClockIcon, "Stopwatch")
export const Clock: Icon = adapt(ClockIcon, "Clock")
export const ClockRewind: Icon = adapt(ArrowUturnLeftIcon, "ClockRewind")
export const Bell: Icon = adapt(BellIcon, "Bell")
export const Bug: Icon = adapt(BugAntIcon, "Bug")
export const Lightning: Icon = adapt(BoltIcon, "Lightning")
export const Inbox: Icon = adapt(InboxIcon, "Inbox")
export const Heart: Icon = adapt(HeartIcon, "Heart")
export const Lifebuoy: Icon = adapt(LifebuoyIcon, "Lifebuoy")

/* The machine, and the things running on it.
 *
 * `Servers` is the whole rack; `Connection` is the radio glyph, because a link
 * already means `Link` in this vocabulary; `Route` is the map. `ChartActivity`
 * is the rising trend and `LineChart` the chart with axes; the sidebar's
 * `Monitoring` is a screen, and so is `DesktopDevice` below. */
export const Servers: Icon = adapt(ServerStackIcon, "Servers")
export const Cpu: Icon = adapt(CpuChipIcon, "Cpu")
export const GridSquare: Icon = adapt(Squares2X2Icon, "GridSquare")
export const Gauge: Icon = adapt(ChartBarIcon, "Gauge")
export const ChartActivity: Icon = adapt(ArrowTrendingUpIcon, "ChartActivity")
export const LineChart: Icon = adapt(PresentationChartLineIcon, "LineChart")
export const Monitoring: Icon = adapt(ComputerDesktopIcon, "Monitoring")
export const NetworkDevice: Icon = adapt(SignalIcon, "NetworkDevice")
export const Connection: Icon = adapt(RadioIcon, "Connection")
export const Router: Icon = adapt(WifiIcon, "Router")
export const Route: Icon = adapt(MapIcon, "Route")
export const Globe: Icon = adapt(GlobeAltIcon, "Globe")
export const Database: Icon = adapt(CircleStackIcon, "Database")
export const Layout: Icon = adapt(RectangleGroupIcon, "Layout")
export const SidebarLeft: Icon = adapt(ViewColumnsIcon, "SidebarLeft")
export const SidebarRight: Icon = adapt(ViewColumnsIcon, "SidebarRight")
export const Footer: Icon = adapt(Bars3Icon, "Footer")
export const ListOrdered: Icon = adapt(NumberedListIcon, "ListOrdered")
export const ListUnordered: Icon = adapt(ListBulletIcon, "ListUnordered")
export const Monorepo: Icon = adapt(Square3Stack3DIcon, "Monorepo")
export const GridMasonry: Icon = adapt(RectangleStackIcon, "GridMasonry")
export const Box: Icon = adapt(CubeIcon, "Box")
export const Layers: Icon = adapt(Square2StackIcon, "Layers")
export const Puzzle: Icon = adapt(PuzzlePieceIcon, "Puzzle")
export const Archive: Icon = adapt(ArchiveBoxIcon, "Archive")
export const Terminal: Icon = adapt(CommandLineIcon, "Terminal")
export const TerminalWindow: Icon = adapt(WindowIcon, "TerminalWindow")
export const Logs: Icon = adapt(QueueListIcon, "Logs")
export const Rss: Icon = adapt(RssIcon, "Rss")
export const Notes: Icon = adapt(ClipboardDocumentListIcon, "Notes")

/* git and deployment. Heroicons draws no git glyphs, so these are the nearest
   true things: a branch is connected nodes coming apart (`Share`), a merge the
   same nodes coming together, a commit the short hash it is shown as, a pull
   request the review conversation, a new branch a plus. */
export const GitBranch: Icon = adapt(ShareIcon, "GitBranch")
export const GitCommit: Icon = adapt(HashtagIcon, "GitCommit")
export const GitMerge: Icon = adapt(ArrowsPointingInIcon, "GitMerge")
export const GitPullRequest: Icon = adapt(ChatBubbleLeftRightIcon, "GitPullRequest")
export const BranchPlus: Icon = adapt(SquaresPlusIcon, "BranchPlus")

/* Security. There is one shield drawing with a check and one with an
   exclamation — `Shield`, `ShieldCheck` and `FirewallCheck` are the former,
   `ShieldOff` the latter. SSH is the key: a shell with a key is what the page
   is about. */
export const Shield: Icon = adapt(ShieldCheckIcon, "Shield")
export const ShieldCheck: Icon = adapt(ShieldCheckIcon, "ShieldCheck")
export const ShieldOff: Icon = adapt(ShieldExclamationIcon, "ShieldOff")
export const FirewallCheck: Icon = adapt(ShieldCheckIcon, "FirewallCheck")
export const SecureConnection: Icon = adapt(KeyIcon, "SecureConnection")
export const LockClosed: Icon = adapt(LockClosedIcon, "LockClosed")
export const LockOpen: Icon = adapt(LockOpenIcon, "LockOpen")
export const Key: Icon = adapt(KeyIcon, "Key")
export const Inspect: Icon = adapt(MagnifyingGlassCircleIcon, "Inspect")
export const Crosshair: Icon = adapt(ViewfinderCircleIcon, "Crosshair")
export const SignIn: Icon = adapt(ArrowRightEndOnRectangleIcon, "SignIn")

/* People and the account menu. Signing out is leaving, so the arrow points at
   the door on the left; signing in points in from the right. */
export const Users: Icon = adapt(UsersIcon, "Users")
export const UserPlus: Icon = adapt(UserPlusIcon, "UserPlus")
export const UserMinus: Icon = adapt(UserMinusIcon, "UserMinus")
export const UserSettings: Icon = adapt(UserCircleIcon, "UserSettings")
export const Logout: Icon = adapt(ArrowLeftStartOnRectangleIcon, "Logout")
export const Home: Icon = adapt(HomeIcon, "Home")
export const DesktopDevice: Icon = adapt(ComputerDesktopIcon, "DesktopDevice")

/* Appearance and view controls. `BlendMode` is the appearance page: the swatch
   is a light/dark drawing, where a painter's palette promises a colour picker
   this product does not have. A pinned session is a bookmark — the thing kept
   where you left it. */
export const BlendMode: Icon = adapt(SwatchIcon, "BlendMode")
export const Sun: Icon = adapt(SunIcon, "Sun")
export const Moon: Icon = adapt(MoonIcon, "Moon")
export const Eye: Icon = adapt(EyeIcon, "Eye")
export const EyeOff: Icon = adapt(EyeSlashIcon, "EyeOff")
export const Star: Icon = adapt(StarIcon, "Star")
export const StarFill: Icon = adapt(StarIcon, "StarFill")
export const Pin: Icon = adapt(BookmarkIcon, "Pin")
export const MoreHorizontal: Icon = adapt(EllipsisHorizontalIcon, "MoreHorizontal")
export const SettingsGear: Icon = adapt(Cog6ToothIcon, "SettingsGear")
export const SettingsSliders: Icon = adapt(AdjustmentsHorizontalIcon, "SettingsSliders")
export const BookOpen: Icon = adapt(BookOpenIcon, "BookOpen")
export const Link: Icon = adapt(LinkIcon, "Link")
export const Linked: Icon = adapt(LinkIcon, "Linked")

/* Generic file and folder chrome — the new-folder button, the empty-state
   sheet, the archive actions. The per-extension vocabulary (what a `.json` or
   a `.jpg` draws in a listing) is Material Design Icons in
   `files/file-icon.tsx`, not here. The `Acronym*` badges survive only where
   they are spent outside the file browser: `AcronymJson` marks JSON in the
   database pages, where it draws structured braces rather than a badge. */
export const File: Icon = adapt(DocumentIcon, "File")
export const FileText: Icon = adapt(DocumentTextIcon, "FileText")
export const FileZip: Icon = adapt(ArchiveBoxArrowDownIcon, "FileZip")
export const FolderClosed: Icon = adapt(FolderIcon, "FolderClosed")
export const FolderOpen: Icon = adapt(FolderOpenIcon, "FolderOpen")
export const FolderPlus: Icon = adapt(FolderPlusIcon, "FolderPlus")
export const FolderMinus: Icon = adapt(FolderMinusIcon, "FolderMinus")
export const PreviewDocument: Icon = adapt(DocumentMagnifyingGlassIcon, "PreviewDocument")
export const Image: Icon = adapt(PhotoIcon, "Image")
export const Video: Icon = adapt(FilmIcon, "Video")
export const Music: Icon = adapt(MusicalNoteIcon, "Music")
export const Location: Icon = adapt(MapPinIcon, "Location")
export const AcronymCsv: Icon = adapt(TableCellsIcon, "AcronymCsv")
export const AcronymGif: Icon = adapt(GifIcon, "AcronymGif")
export const AcronymJpg: Icon = adapt(PhotoIcon, "AcronymJpg")
export const AcronymJson: Icon = adapt(CodeBracketIcon, "AcronymJson")
export const AcronymMarkdown: Icon = adapt(DocumentTextIcon, "AcronymMarkdown")
export const AcronymSvg: Icon = adapt(PaintBrushIcon, "AcronymSvg")
