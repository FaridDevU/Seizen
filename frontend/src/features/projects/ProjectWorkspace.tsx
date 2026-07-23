import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type CSSProperties,
  type FormEvent,
  type MouseEvent as ReactMouseEvent,
  type PointerEvent as ReactPointerEvent,
  type WheelEvent as ReactWheelEvent,
} from "react"
import {
  ArrowLeft,
  ArrowRight,
  ArrowUp,
  Check,
  ChevronRight,
  CircleAlert,
  CircleHelp,
  Command as CommandIcon,
  Download,
  FileText,
  FolderOpen,
  Globe2,
  ImagePlus,
  ListChecks,
  LoaderCircle,
  Maximize2,
  Menu,
  Minimize2,
  Music2,
  Palette,
  PanelsTopLeft,
  Pause,
  Pencil,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Settings,
  SkipBack,
  SkipForward,
  SquareTerminal,
  StickyNote,
  Trash2,
  X,
  type LucideIcon,
} from "lucide-react"
import {
  EventsOn,
  WindowToggleMaximise,
} from "../../../wailsjs/runtime/runtime"
import {
  AskWorkspaceAssistant,
  ControlSpotifyPlayback,
  GetSpotifyPlaybackSince,
  GetEditorIntegrations,
  GetSpotifyPlayback,
  FocusNativeEditor,
  StartNativeEditor,
  StopProjectEditor,
} from "../../../wailsjs/go/core/App"
import type { core } from "../../../wailsjs/go/models"
import { AssistantChat, type ChatMessage, type ChatSize } from "@/components/AssistantChat"

import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { confirmDialog, promptDialog } from "@/components/ui/confirm"
import { WindowControls } from "@/components/WindowControls"
import { BrandChip, brandGlyphs } from "@/components/ui/brand-icon"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

import {
  projectService,
  type Project,
  type ProjectContext,
} from "./project-service"
import {
  registerWorkspaceSuspender,
  stopProjectInOrder,
} from "./workspace-lifecycle"
import {
  clampZoom,
  documentKinds,
  findFreePosition,
  fitWorkspaceViewport,
  maximumNoteCharacters,
  snapDragPosition,
  tidyGap,
  tidyLayout,
  maximumTodoItemCharacters,
  maximumTodoItems,
  noteColors,
  maximumRegionLabel,
  normalizeBrowserURL,
  normalizeEditorURL,
  parseWorkspace,
  regionContaining,
  serializeWorkspace,
  workspaceAssetURL,
  type StoredRegion,
  type NoteColor,
  type StoredBrowserNode,
  type StoredDocumentNode,
  type StoredEditorNode,
  type StoredNoteNode,
  type StoredPhotoNode,
  type StoredPlayerNode,
  type StoredTerminalNode,
  type StoredTodoNode,
  type TerminalShell,
  type TodoItem,
  type WorkspaceViewport,
} from "./workspace-model"
import { DocumentPanel } from "./DocumentPanel"
import { subscribeToFileDrops } from "./file-drop"
import { notifyInBackground } from "./notifications"
import {
  isWorkspaceActionDetail,
  takeQuickAction,
  workspaceActionEvent,
  type WorkspaceQuickAction,
} from "./workspace-actions"
import {
  RealTerminal,
  type RealTerminalHandle,
} from "./RealTerminal"
import { AppView, type AppTerminalSession } from "./AppView"
import {
  ProjectModeSelector,
  type ProjectMode,
} from "./ProjectModeSelector"
import { ServerLabView } from "./ServerLabView"

type TerminalStatus = "starting" | "running" | "exited" | "error"

type TerminalNode = StoredTerminalNode & {
  sessionId?: string
  status: TerminalStatus
  error?: string
  // First line of the task the assistant delegated here; shown as the panel title.
  taskHint?: string
}

type BrowserNode = StoredBrowserNode
type PlayerNode = StoredPlayerNode
type PhotoNode = StoredPhotoNode & {
  dataURL?: string
  status: "loading" | "ready" | "error"
  error?: string
}
type EditorStatus = "starting" | "running" | "error"
type EditorNode = StoredEditorNode & {
  sessionId?: string
  url?: string
  // Editors without a web UI: their Win32 window is embedded and follows the node's rect.
  native?: boolean
  status: EditorStatus
  error?: string
}
type NoteNode = StoredNoteNode
type TodoNode = StoredTodoNode
type DocumentNode = StoredDocumentNode
type WorkspaceNode =
  | TerminalNode
  | BrowserNode
  | PlayerNode
  | PhotoNode
  | EditorNode
  | NoteNode
  | TodoNode
  | DocumentNode
type WorkspaceNotice = { tone: "success" | "error"; message: string }
type PendingTerminal = {
  output: string
  exited: boolean
  error: string
  truncated: boolean
}
type BufferedTerminalOutput = Pick<PendingTerminal, "output" | "truncated">
type SpotifyPlayback = Awaited<ReturnType<typeof GetSpotifyPlayback>>
type SpotifyAction = "previous" | "toggle" | "next" | "refresh"

const terminalTitles: Record<TerminalShell, string> = {
  cmd: "CMD",
  wsl: "WSL",
  codex: "Codex",
  claude: "Claude",
  opencode: "OpenCode",
}

const editorTitles: Record<string, string> = {
  vscode: "VS Code · Seizen",
  cursor: "Cursor",
  antigravity: "Antigravity",
  zed: "Zed",
}

function PanelIcon({ node }: { node: WorkspaceNode }) {
  const brandKey =
    node.type === "player"
      ? "spotify"
      : node.type === "terminal"
        ? node.shell
        : node.type === "editor"
          ? node.editorId
          : ""

  if (brandGlyphs[brandKey]) {
    return (
      <BrandChip
        brand={brandKey}
        className="size-6 rounded-[0.45rem]"
        iconClassName="size-3.5"
      />
    )
  }

  const Fallback =
    node.type === "terminal"
      ? SquareTerminal
      : node.type === "browser"
        ? Globe2
        : node.type === "photo"
          ? ImagePlus
          : node.type === "note"
            ? StickyNote
            : node.type === "todo"
              ? ListChecks
              : node.type === "document"
                ? FileText
                : Pencil

  return (
    <span
      aria-hidden="true"
      className="flex size-6 shrink-0 items-center justify-center rounded-[0.45rem] bg-[var(--primary-container)] text-[var(--on-primary-container)]"
    >
      <Fallback className="size-3.5" strokeWidth={1.7} />
    </span>
  )
}

type Interaction =
  | {
      kind: "pan"
      pointerId: number
      startClientX: number
      startClientY: number
      startX: number
      startY: number
    }
  | {
      kind: "move"
      pointerId: number
      id: string
      startClientX: number
      startClientY: number
      startX: number
      startY: number
      zoom: number
    }
  | {
      kind: "resize"
      pointerId: number
      id: string
      startClientX: number
      startClientY: number
      startWidth: number
      startHeight: number
      zoom: number
    }
  | {
      kind: "region-move"
      pointerId: number
      id: string
      startClientX: number
      startClientY: number
      startX: number
      startY: number
      zoom: number
      members: Array<{ id: string; startX: number; startY: number }>
    }
  | {
      kind: "region-resize"
      pointerId: number
      id: string
      startClientX: number
      startClientY: number
      startWidth: number
      startHeight: number
      zoom: number
    }

const minimumNodeWidth = 280
const minimumNodeHeight = 190
const maximumHistoryEntries = 100

type GeometrySnapshot = Map<
  string,
  { x: number; y: number; z: number; width: number; height: number }
>

function snapshotGeometry(nodes: readonly WorkspaceNode[]): GeometrySnapshot {
  return new Map(
    nodes.map((node) => [
      node.id,
      { x: node.x, y: node.y, z: node.z, width: node.width, height: node.height },
    ]),
  )
}
// JavaScript stores strings as UTF-16, so this is roughly 256 KiB per panel.
const maximumTerminalCharacters = 128 * 1024
const spotifyPollDelay = 2_000

export type WorkspaceDockItem = {
  id: string
  name: string
  thumbnail?: string
  live: boolean
  active: boolean
}

export type WorkspaceDockCandidate = {
  id: string
  name: string
  thumbnail?: string
  open: boolean
}

export type WorkspaceDock = {
  items: WorkspaceDockItem[]
  candidates: WorkspaceDockCandidate[]
  onSelect: (projectId: string) => void
  onClose: (projectId: string) => void
  onOpenProject: (projectId: string) => void
}

// Compiz-style wobble rendering: warp a panel onto its four displaced corners
// with a projective transform (adjugate method) expressed as a CSS matrix3d.
function adjugate(m: number[]) {
  return [
    m[4] * m[8] - m[5] * m[7],
    m[2] * m[7] - m[1] * m[8],
    m[1] * m[5] - m[2] * m[4],
    m[5] * m[6] - m[3] * m[8],
    m[0] * m[8] - m[2] * m[6],
    m[2] * m[3] - m[0] * m[5],
    m[3] * m[7] - m[4] * m[6],
    m[1] * m[6] - m[0] * m[7],
    m[0] * m[4] - m[1] * m[3],
  ]
}

function multiplyMatrices(a: number[], b: number[]) {
  const product = new Array<number>(9)
  for (let row = 0; row < 3; row++) {
    for (let column = 0; column < 3; column++) {
      product[3 * row + column] =
        a[3 * row] * b[column] +
        a[3 * row + 1] * b[column + 3] +
        a[3 * row + 2] * b[column + 6]
    }
  }
  return product
}

function basisToPoints(points: number[]) {
  const [x1, y1, x2, y2, x3, y3, x4, y4] = points
  const m = [x1, x2, x3, y1, y2, y3, 1, 1, 1]
  const a = adjugate(m)
  const v = [
    a[0] * x4 + a[1] * y4 + a[2],
    a[3] * x4 + a[4] * y4 + a[5],
    a[6] * x4 + a[7] * y4 + a[8],
  ]
  return multiplyMatrices(m, [v[0], 0, 0, 0, v[1], 0, 0, 0, v[2]])
}

// corners: TL, TR, BL, BR as flat [x, y, ...] pairs.
function quadWarp(width: number, height: number, corners: number[]) {
  const source = basisToPoints([0, 0, width, 0, 0, height, width, height])
  const target = basisToPoints(corners)
  const t = multiplyMatrices(target, adjugate(source))
  if (!t[8]) return ""
  for (let i = 0; i < 9; i++) t[i] /= t[8]
  return `matrix3d(${t[0]}, ${t[3]}, 0, ${t[6]}, ${t[1]}, ${t[4]}, 0, ${t[7]}, 0, 0, 1, 0, ${t[2]}, ${t[5]}, 0, 1)`
}

function ProjectWorkspace({
  project,
  initialMode = "workspace",
  initialServerId,
  dock,
  onBack,
  onDownload,
  onEdit,
  onOpenSettings,
  onOpenCommandMenu,
  onDelete,
  onOpenFolder,
}: {
  project: Project
  initialMode?: ProjectMode
  initialServerId?: string
  dock?: WorkspaceDock
  onBack: () => void
  onDownload: () => Promise<string>
  onEdit: () => void
  onOpenSettings: () => void
  onOpenCommandMenu: () => void
  onDelete: () => void
  onOpenFolder: () => Promise<void>
}) {
  const [nodes, setNodes] = useState<WorkspaceNode[]>([])
  const [regions, setRegions] = useState<StoredRegion[]>([])
  const [viewport, setViewport] = useState<WorkspaceViewport>({
    x: 80,
    y: 88,
    zoom: 1,
  })
  const [selectedID, setSelectedID] = useState<string | null>(null)
  const [loaded, setLoaded] = useState(false)
  const [command, setCommand] = useState("")
  const [commandMessage, setCommandMessage] = useState("")
  const [showHelp, setShowHelp] = useState(false)
  const [workspaceBackground, setWorkspaceBackground] = useState("")
  const [backgroundBusy, setBackgroundBusy] = useState(false)
  const [photoBusy, setPhotoBusy] = useState(false)
  const [isPanning, setIsPanning] = useState(false)
  const [notice, setNotice] = useState<WorkspaceNotice | null>(null)
  const [dockPickerOpen, setDockPickerOpen] = useState(false)
  const [dockQuery, setDockQuery] = useState("")
  const dockRef = useRef<HTMLElement>(null)

  useEffect(() => {
    if (!dockPickerOpen) return
    const handleOutsidePress = (event: PointerEvent) => {
      if (!dockRef.current?.contains(event.target as Node)) {
        setDockPickerOpen(false)
      }
    }
    document.addEventListener("pointerdown", handleOutsidePress)
    return () => document.removeEventListener("pointerdown", handleOutsidePress)
  }, [dockPickerOpen])
  const [maximizedID, setMaximizedID] = useState<string | null>(null)
  const [projectMode, setProjectMode] = useState<ProjectMode>(initialMode)
  const [activeContext, setActiveContext] = useState<ProjectContext>({
    projectId: project.id,
    experimentId: "",
    name: "Principal",
    kind: "main",
    branchName: project.branch ?? "",
    path: project.path,
    status: "active",
  })
  const [selectedAppId, setSelectedAppId] = useState("")
  const [editorIntegrations, setEditorIntegrations] = useState<
    core.EditorIntegration[]
  >([])
  const [workspaceMenu, setWorkspaceMenu] = useState<{
    x: number
    y: number
    submenuSide: "left" | "right"
  } | null>(null)
  // The most recently used add-menu group renders first: fresh installs see
  // Documents on top, developers who reach for terminals daily keep them on top.
  const [menuGroupOrder, setMenuGroupOrder] = useState<string[]>(readMenuGroupOrder)
  const recordMenuGroupUse = (group: string) => {
    setMenuGroupOrder((current) => {
      const next = [group, ...current.filter((key) => key !== group)]
      try {
        localStorage.setItem(menuGroupOrderKey, JSON.stringify(next))
      } catch {
        // Ordering is a convenience; storage failures shouldn't break the menu.
      }
      return next
    })
  }

  const canvasRef = useRef<HTMLDivElement>(null)
  const interactionRef = useRef<Interaction | null>(null)
  const nodesRef = useRef(nodes)
  const viewportRef = useRef(viewport)
  // During drag/pan/zoom the state isn't touched: the DOM is written directly and the
  // pending value lives in these refs; it's committed once on release.
  const viewportOverrideRef = useRef<WorkspaceViewport | null>(null)
  const nodeOverrideRef = useRef<{
    id: string
    x?: number
    y?: number
    width?: number
    height?: number
  } | null>(null)
  // A dragged region carries its member panels; the pending positions live here
  // until release, mirroring nodeOverrideRef for single panels.
  const regionOverrideRef = useRef<{
    id: string
    x?: number
    y?: number
    width?: number
    height?: number
    members?: Map<string, { x: number; y: number }>
  } | null>(null)
  const panelElsRef = useRef(new Map<string, HTMLElement>())
  // Wobbly windows (Compiz-style): four corner masses on damped springs. Drag
  // impulses displace each corner in proportion to its distance from the grab
  // point — the grabbed corner sticks to the pointer, the far ones trail
  // behind — and the panel is warped onto the displaced quad. Driven straight
  // from pointer moves and written imperatively — no re-renders.
  const wobblesRef = useRef(
    new Map<
      string,
      {
        lastX: number
        lastY: number
        width: number
        height: number
        // TL, TR, BL, BR: spring offset, velocity, and lag weight per corner.
        corners: { ox: number; oy: number; vx: number; vy: number; w: number }[]
        raf: number
        prev: number
        active: boolean
      }
    >(),
  )

  const stepWobble = (id: string, now: number) => {
    const wobble = wobblesRef.current.get(id)
    if (!wobble) return
    const element = panelElsRef.current.get(id)
    const dt = Math.min((now - wobble.prev) / 1000, 1 / 30)
    wobble.prev = now
    const stiffness = 180
    const damping = 10
    let energy = 0
    for (const corner of wobble.corners) {
      corner.vx += (-stiffness * corner.ox - damping * corner.vx) * dt
      corner.vy += (-stiffness * corner.oy - damping * corner.vy) * dt
      corner.ox += corner.vx * dt
      corner.oy += corner.vy * dt
      energy = Math.max(
        energy,
        Math.abs(corner.ox),
        Math.abs(corner.oy),
        Math.abs(corner.vx) / 10,
        Math.abs(corner.vy) / 10,
      )
    }
    // While the pointer is still dragging, the loop must stay alive even when
    // the springs momentarily rest — the next impulse can arrive any time.
    if (!wobble.active && energy < 0.1) {
      if (element) {
        element.style.transform = ""
        element.style.transformOrigin = ""
      }
      wobblesRef.current.delete(id)
      return
    }
    if (element) {
      const [tl, tr, bl, br] = wobble.corners
      element.style.transform = quadWarp(wobble.width, wobble.height, [
        tl.ox,
        tl.oy,
        wobble.width + tr.ox,
        tr.oy,
        bl.ox,
        wobble.height + bl.oy,
        wobble.width + br.ox,
        wobble.height + br.oy,
      ])
    }
    wobble.raf = requestAnimationFrame((next) => stepWobble(id, next))
  }

  const grabWobble = (
    id: string,
    x: number,
    y: number,
    grabX: number,
    grabY: number,
  ) => {
    if (document.documentElement.dataset.wobbly !== "on") return
    const element = panelElsRef.current.get(id)
    if (!element) return
    // Corner lag grows with distance from the grab point, so the grabbed spot
    // follows the pointer and the far corners trail behind.
    const weights = [
      [0, 0],
      [1, 0],
      [0, 1],
      [1, 1],
    ].map(([cx, cy]) => Math.min(1, Math.hypot(cx - grabX, cy - grabY)) * 0.55)
    let wobble = wobblesRef.current.get(id)
    if (!wobble) {
      wobble = {
        lastX: x,
        lastY: y,
        width: element.offsetWidth,
        height: element.offsetHeight,
        corners: weights.map((w) => ({ ox: 0, oy: 0, vx: 0, vy: 0, w })),
        raf: 0,
        prev: performance.now(),
        active: true,
      }
      wobblesRef.current.set(id, wobble)
      wobble.raf = requestAnimationFrame((now) => stepWobble(id, now))
    } else {
      // Re-grabbed while still settling: keep the motion, refresh the grip.
      wobble.lastX = x
      wobble.lastY = y
      wobble.width = element.offsetWidth
      wobble.height = element.offsetHeight
      wobble.active = true
      wobble.corners.forEach((corner, index) => {
        corner.w = weights[index]
      })
    }
    element.style.transformOrigin = "0 0"
  }

  const wobblePanel = (id: string, x: number, y: number) => {
    const wobble = wobblesRef.current.get(id)
    if (!wobble) return
    const dx = x - wobble.lastX
    const dy = y - wobble.lastY
    wobble.lastX = x
    wobble.lastY = y
    const limit = Math.min(90, wobble.width * 0.3, wobble.height * 0.3)
    for (const corner of wobble.corners) {
      corner.ox = Math.max(-limit, Math.min(limit, corner.ox - dx * corner.w))
      corner.oy = Math.max(-limit, Math.min(limit, corner.oy - dy * corner.w))
    }
  }

  const releaseWobble = (id: string) => {
    const wobble = wobblesRef.current.get(id)
    if (wobble) wobble.active = false
  }

  useEffect(
    () => () => {
      for (const wobble of wobblesRef.current.values()) {
        if (wobble.raf) cancelAnimationFrame(wobble.raf)
      }
    },
    [],
  )

  // Freshly spawned panels of any type pop in on the same springs, gated by
  // the same settings toggle as drag wobble: corners start pulled inward and
  // jiggle out to rest. CSS keeps the opacity fade (see .wobble-pop).
  const spawnWobble = (id: string) => {
    if (document.documentElement.dataset.wobbly !== "on") return
    const element = panelElsRef.current.get(id)
    if (!element) return
    const width = element.offsetWidth
    const height = element.offsetHeight
    if (!width || !height) return
    const previous = wobblesRef.current.get(id)
    if (previous?.raf) cancelAnimationFrame(previous.raf)
    const inset = Math.min(28, width * 0.1, height * 0.1)
    const wobble = {
      lastX: 0,
      lastY: 0,
      width,
      height,
      corners: [
        [1, 1],
        [-1, 1],
        [1, -1],
        [-1, -1],
      ].map(([sx, sy]) => ({
        ox: sx * inset,
        oy: sy * inset,
        vx: 0,
        vy: 0,
        w: 0,
      })),
      raf: 0,
      prev: performance.now(),
      active: false,
    }
    element.style.transformOrigin = "0 0"
    wobblesRef.current.set(id, wobble)
    wobble.raf = requestAnimationFrame((now) => stepWobble(id, now))
  }

  // Ids present now but missing from the previous pass are freshly spawned;
  // a workspace (re)load resets the baseline so nothing pops en masse.
  const seenNodeIdsRef = useRef<Set<string> | null>(null)
  useEffect(() => {
    if (!loaded) {
      seenNodeIdsRef.current = null
      return
    }
    const seen = seenNodeIdsRef.current
    seenNodeIdsRef.current = new Set(nodes.map((node) => node.id))
    if (!seen) return
    for (const node of nodes) {
      if (!seen.has(node.id)) spawnWobble(node.id)
    }
  }, [nodes, loaded])
  const panelsLayerRef = useRef<HTMLDivElement>(null)
  const gridFineRef = useRef<HTMLDivElement>(null)
  const gridMajorRef = useRef<HTMLDivElement>(null)
  const wheelCommitTimerRef = useRef<number>(undefined)

  const applyViewportDOM = (next: WorkspaceViewport) => {
    if (panelsLayerRef.current) {
      panelsLayerRef.current.style.transform = `translate3d(${next.x}px, ${next.y}px, 0) scale(${next.zoom})`
    }
    if (gridFineRef.current) {
      gridFineRef.current.style.backgroundPosition = `${next.x}px ${next.y}px`
      gridFineRef.current.style.backgroundSize = `${20 * next.zoom}px ${20 * next.zoom}px`
    }
    if (gridMajorRef.current) {
      gridMajorRef.current.style.backgroundPosition = `${next.x}px ${next.y}px`
      gridMajorRef.current.style.backgroundSize = `${100 * next.zoom}px ${100 * next.zoom}px`
    }
  }
  const workspaceGeneration = useRef(0)
  const sessionNodesRef = useRef(new Map<string, string>())
  const pendingTerminalsRef = useRef(new Map<string, PendingTerminal>())
  const terminalViewsRef = useRef(new Map<string, RealTerminalHandle>())
  const bufferedTerminalOutputRef = useRef(
    new Map<string, BufferedTerminalOutput>(),
  )
  const closedTerminalNodesRef = useRef(new Set<string>())
  const terminalFocusRequestsRef = useRef(new Set<string>())
  const cancelledTerminalStartsRef = useRef(new Set<string>())
  const terminalStartPromisesRef = useRef(new Set<Promise<unknown>>())
  const editorSessionNodesRef = useRef(new Map<string, string>())
  const preparedEditorRef = useRef<{
    generation: number
    promise: Promise<core.EditorSession>
  } | null>(null)
  const pendingEditorExitsRef = useRef(
    new Map<string, { exitCode: number; error: string }>(),
  )
  const cancelledEditorStartsRef = useRef(new Set<string>())
  const editorStartPromisesRef = useRef(new Set<Promise<unknown>>())
  const suspendPromiseRef = useRef<Promise<void> | null>(null)
  const mountedRef = useRef(true)
  const loadedRef = useRef(false)
  const saveTimerRef = useRef<number | undefined>(undefined)
  const projectMenuRef = useRef<HTMLDetailsElement>(null)
  const projectMenuSummaryRef = useRef<HTMLElement>(null)
  const workspaceMenuRef = useRef<HTMLDivElement>(null)
  const backgroundDetailsRef = useRef<HTMLDetailsElement>(null)
  const backgroundSummaryRef = useRef<HTMLElement>(null)
  const zoomDetailsRef = useRef<HTMLDetailsElement>(null)
  const zoomSummaryRef = useRef<HTMLElement>(null)
  const saveQueueRef = useRef<Promise<void>>(Promise.resolve())
  const latestLayoutRef = useRef("")
  const lastQueuedLayoutRef = useRef("")
  const historyRef = useRef<GeometrySnapshot[]>([])
  const futureRef = useRef<GeometrySnapshot[]>([])
  const [droppingFiles, setDroppingFiles] = useState(false)
  const [showFirstRunHint, setShowFirstRunHint] = useState(() => {
    try {
      return !localStorage.getItem(firstRunHintKey)
    } catch {
      return false
    }
  })
  const dismissFirstRunHint = () => {
    setShowFirstRunHint(false)
    try {
      localStorage.setItem(firstRunHintKey, "1")
    } catch {
      // The hint just reappears next launch; nothing breaks.
    }
  }

  nodesRef.current = nodes
  viewportRef.current = viewport
  loadedRef.current = loaded
  const regionsRef = useRef(regions)
  regionsRef.current = regions
  const regionElsRef = useRef(new Map<string, HTMLElement>())
  const terminalOutputAtRef = useRef(new Map<string, number>())

  useEffect(() => setProjectMode(initialMode), [initialMode, project.id])

  useEffect(() => {
    let mounted = true
    setActiveContext({
      projectId: project.id,
      experimentId: "",
      name: "Principal",
      kind: "main",
      branchName: project.branch ?? "",
      path: project.path,
      status: "active",
    })
    void projectService.getProjectContext(project.id)
      .then((context) => {
        if (mounted) setActiveContext(context)
      })
      .catch((error: unknown) => {
        if (mounted) setNotice({ tone: "error", message: errorMessage(error) })
      })
    const off = EventsOn("experiment.selected", (payload: unknown) => {
      if (!isRecord(payload) || payload.projectId !== project.id) return
      void projectService.getProjectContext(project.id).then((context) => {
        if (mounted) setActiveContext(context)
      })
    })
    return () => {
      mounted = false
      off()
    }
  }, [project.branch, project.id, project.path])

  const selectExperiment = async (experimentId: string) => {
    window.clearTimeout(saveTimerRef.current)
    await saveQueueRef.current
    if (loadedRef.current && latestLayoutRef.current) {
      await projectService.saveProjectWorkspace(
        project,
        latestLayoutRef.current,
        activeContext.experimentId,
      )
    }
    setActiveContext(await projectService.selectProjectExperiment(project.id, experimentId))
  }

  useEffect(() => {
    let cancelled = false
    let poll: number | undefined
    const loadEditorIntegrations = () => {
      void GetEditorIntegrations()
        .then((integrations) => {
          if (cancelled) return
          setEditorIntegrations(integrations)
          const vscode = integrations.find((editor) => editor.id === "vscode")
          if (vscode?.enabled && !vscode.available && vscode.status === "installing") {
            poll = window.setTimeout(loadEditorIntegrations, 1_000)
          }
        })
        .catch((error: unknown) => {
          if (!cancelled) {
            setNotice({ tone: "error", message: errorMessage(error) })
          }
        })
    }
    loadEditorIntegrations()
    return () => {
      cancelled = true
      if (poll !== undefined) window.clearTimeout(poll)
    }
  }, [project.id])

  const serializedLayout = useMemo(
    () => serializeWorkspace(viewport, nodes, regions),
    [nodes, regions, viewport],
  )
  latestLayoutRef.current = serializedLayout

  const queueSave = (
    target: Project,
    value: string,
    experimentId: string,
    afterSave?: () => Promise<void>,
  ) => {
    saveQueueRef.current = saveQueueRef.current
      .catch(() => undefined)
      .then(async () => {
        await projectService.saveProjectWorkspace(target, value, experimentId)
        await afterSave?.()
      })
      .catch((error: unknown) => {
        if (lastQueuedLayoutRef.current === value) {
          lastQueuedLayoutRef.current = ""
        }
        if (mountedRef.current) {
          setNotice({ tone: "error", message: errorMessage(error) })
        }
      })
  }

  const writeTerminalOutput = (
    nodeID: string,
    data: string,
    alreadyTruncated = false,
  ) => {
    terminalOutputAtRef.current.set(nodeID, Date.now())
    const view = terminalViewsRef.current.get(nodeID)
    if (view) {
      if (alreadyTruncated) {
        view.write("\r\n[Part of the earlier output was skipped.]\r\n")
      }
      if (data) view.write(data)
      return
    }

    const current = bufferedTerminalOutputRef.current.get(nodeID) ?? {
      output: "",
      truncated: false,
    }
    const combined = current.output + data
    bufferedTerminalOutputRef.current.set(nodeID, {
      output: combined.slice(-maximumTerminalCharacters),
      truncated:
        current.truncated ||
        alreadyTruncated ||
        combined.length > maximumTerminalCharacters,
    })
  }

  const registerTerminalView = (
    nodeID: string,
    view: RealTerminalHandle | null,
  ) => {
    if (!view) {
      terminalViewsRef.current.delete(nodeID)
      return
    }
    terminalViewsRef.current.set(nodeID, view)
    if (terminalFocusRequestsRef.current.delete(nodeID)) view.focus()
    const buffered = bufferedTerminalOutputRef.current.get(nodeID)
    if (!buffered) return
    bufferedTerminalOutputRef.current.delete(nodeID)
    if (buffered.truncated) {
      view.write("\r\n[Part of the earlier output was skipped.]\r\n")
    }
    if (buffered.output) view.write(buffered.output)
  }

  const startTerminal = async (
    id: string,
    shell: TerminalShell,
    generation = workspaceGeneration.current,
    subfolder = "",
    task = "",
  ): Promise<string | undefined> => {
    const cancellationKey = `${generation}:${id}`
    try {
      const sessionId =
        shell === "codex" || shell === "claude"
          ? await projectService.startProjectAgentTerminal(
              project,
              shell,
              selectedAppId,
              activeContext.experimentId,
              task,
            )
          : subfolder
            ? await projectService.startProjectTerminalInFolder(
                project,
                shell,
                activeContext.experimentId,
                subfolder,
              )
            : await projectService.startProjectTerminal(
                project,
                shell,
                activeContext.experimentId,
              )
      const cancelled = cancelledTerminalStartsRef.current.delete(cancellationKey)
      if (
        generation !== workspaceGeneration.current ||
        !mountedRef.current ||
        cancelled
      ) {
        await projectService
          .stopProjectTerminal(sessionId)
          .catch(() => undefined)
          .finally(() => pendingTerminalsRef.current.delete(sessionId))
        return undefined
      }
      sessionNodesRef.current.set(sessionId, id)
      const pending = pendingTerminalsRef.current.get(sessionId)
      pendingTerminalsRef.current.delete(sessionId)
      if (pending?.output || pending?.truncated) {
        writeTerminalOutput(id, pending.output, pending.truncated)
      }
      if (pending?.exited) {
        sessionNodesRef.current.delete(sessionId)
        writeTerminalOutput(
          id,
          pending.error
            ? `\r\n[Terminal exited: ${pending.error}]\r\n`
            : "\r\n[Terminal exited]\r\n",
        )
      }
      setNodes((current) =>
        current.map((node) =>
          node.id === id && node.type === "terminal"
            ? {
                ...node,
                sessionId: pending?.exited ? undefined : sessionId,
                status: pending?.exited
                  ? pending.error
                    ? "error"
                    : "exited"
                  : "running",
                error: pending?.error || undefined,
              }
            : node,
        ),
      )
      const view = terminalViewsRef.current.get(id)
      if (view && terminalFocusRequestsRef.current.delete(id)) view.focus()
      return sessionId
    } catch (error) {
      const cancelled = cancelledTerminalStartsRef.current.delete(cancellationKey)
      if (generation !== workspaceGeneration.current || cancelled) return
      const message = errorMessage(error)
      terminalFocusRequestsRef.current.delete(id)
      writeTerminalOutput(id, `\r\n[Could not start: ${message}]\r\n`)
      setNodes((current) =>
        current.map((node) =>
          node.id === id && node.type === "terminal"
            ? {
                ...node,
                status: "error",
                error: message,
              }
            : node,
        ),
      )
      return undefined
    }
  }

  const launchTerminal = (
    id: string,
    shell: TerminalShell,
    generation = workspaceGeneration.current,
    subfolder = "",
    task = "",
  ) => {
    const start = startTerminal(id, shell, generation, subfolder, task)
    terminalStartPromisesRef.current.add(start)
    void start.finally(() => terminalStartPromisesRef.current.delete(start))
    return start
  }

  const startEditor = async (
    id: string,
    editorId: string,
    generation = workspaceGeneration.current,
  ): Promise<string | undefined> => {
    const cancellationKey = `${generation}:${id}`
    let sessionId = ""
    // Without a web UI (Zed, Cursor, Antigravity...) the session embeds the editor's
    // native window; with a web UI (VS Code) it goes through serve-web + iframe.
    const integration = editorIntegrations.find(
      (editor) => editor.id === editorId,
    )
    const native = integration ? !integration.embedded : editorId !== "vscode"
    try {
      let session: core.EditorSession
      if (native) {
        session = await StartNativeEditor(project.path, editorId)
      } else {
        const sessionPromise = prepareEditorSession(editorId, generation)
        session = await sessionPromise
        if (preparedEditorRef.current?.promise === sessionPromise) {
          preparedEditorRef.current = null
        }
      }
      sessionId = session.sessionId
      if (!sessionId) throw new Error("The editor did not return a valid session")
      const url = native ? undefined : normalizeEditorURL(session.url)
      editorSessionNodesRef.current.set(sessionId, id)
      const pendingExit = pendingEditorExitsRef.current.get(sessionId)
      if (pendingExit) {
        pendingEditorExitsRef.current.delete(sessionId)
        editorSessionNodesRef.current.delete(sessionId)
        throw new Error(
          editorExitMessage(pendingExit, editorTitles[editorId] ?? editorId),
        )
      }
      const cancelled = cancelledEditorStartsRef.current.delete(cancellationKey)
      if (
        generation !== workspaceGeneration.current ||
        !mountedRef.current ||
        cancelled
      ) {
        editorSessionNodesRef.current.delete(sessionId)
        await StopProjectEditor(sessionId).catch(() => undefined)
        return undefined
      }
      setNodes((current) =>
        current.map((node) =>
          node.id === id && node.type === "editor"
            ? { ...node, sessionId, url, native, status: "running", error: undefined }
            : node,
        ),
      )
      return sessionId
    } catch (error) {
      const cancelled = cancelledEditorStartsRef.current.delete(cancellationKey)
      if (sessionId) {
        editorSessionNodesRef.current.delete(sessionId)
        await StopProjectEditor(sessionId).catch(() => undefined)
      }
      if (generation !== workspaceGeneration.current || cancelled) return
      const message = errorMessage(error)
      setNodes((current) =>
        current.map((node) =>
          node.id === id && node.type === "editor"
            ? { ...node, status: "error", error: message }
            : node,
        ),
      )
      return undefined
    }
  }

  const launchEditor = (
    id: string,
    editorId: string,
    generation = workspaceGeneration.current,
  ) => {
    const start = startEditor(id, editorId, generation)
    editorStartPromisesRef.current.add(start)
    void start.finally(() => editorStartPromisesRef.current.delete(start))
    return start
  }

  function prepareEditorSession(
    editorId: string,
    generation = workspaceGeneration.current,
  ) {
    const current = preparedEditorRef.current
    if (current?.generation === generation) return current.promise

    const promise = projectService.startProjectEditor(
      project.id,
      activeContext.experimentId,
      editorId,
    )
    preparedEditorRef.current = { generation, promise }
    editorStartPromisesRef.current.add(promise)
    void promise
      .catch(() => {
        if (preparedEditorRef.current?.promise === promise) {
          preparedEditorRef.current = null
        }
      })
      .finally(() => editorStartPromisesRef.current.delete(promise))
    return promise
  }

  useEffect(() => {
    const vscode = editorIntegrations.find((editor) => editor.id === "vscode")
    if (
      !loaded ||
      !vscode?.enabled ||
      !vscode.available ||
      nodesRef.current.some((node) => node.type === "editor")
    ) {
      return
    }
    void prepareEditorSession("vscode").catch(() => undefined)
  }, [loaded, project.id, project.path, editorIntegrations])

  useEffect(() => {
    mountedRef.current = true
    return () => {
      mountedRef.current = false
    }
  }, [])

  useEffect(() => {
    const offOutput = EventsOn("seizen:terminal-output", (payload: unknown) => {
      if (!isTerminalOutput(payload)) return
      const nodeID = sessionNodesRef.current.get(payload.sessionId)
      if (!nodeID) {
        bufferPendingTerminal(payload.sessionId, payload.data, undefined)
        return
      }
      if (closedTerminalNodesRef.current.has(nodeID)) return
      writeTerminalOutput(nodeID, payload.data)
    })
    const offExit = EventsOn("seizen:terminal-exit", (payload: unknown) => {
      if (!isTerminalExit(payload)) return
      const nodeID = sessionNodesRef.current.get(payload.sessionId)
      if (!nodeID) {
        bufferPendingTerminal(payload.sessionId, "", payload.error)
        return
      }
      sessionNodesRef.current.delete(payload.sessionId)
      if (closedTerminalNodesRef.current.has(nodeID)) {
        bufferedTerminalOutputRef.current.delete(nodeID)
        return
      }
      const exited = nodesRef.current.find((node) => node.id === nodeID)
      if (
        exited?.type === "terminal" &&
        (exited.shell === "claude" || exited.shell === "codex" || exited.shell === "opencode")
      ) {
        void notifyInBackground(
          `${terminalTitles[exited.shell]} finished`,
          payload.error
            ? `The agent stopped with an error in ${project.name}`
            : `The agent finished its work in ${project.name}`,
        )
      }
      // A delegated task ended: close the loop in the chat that launched it.
      if (exited?.type === "terminal" && exited.taskHint) {
        setWsChatMessages((messages) => [
          ...messages,
          payload.error
            ? {
                role: "assistant",
                content: `The task "${exited.taskHint}" stopped with an error.`,
                error: true,
              }
            : {
                role: "assistant",
                content: `Task finished: "${exited.taskHint}" — its results note should be on the board.`,
                chips: ["✓ done"],
              },
        ])
      }
      writeTerminalOutput(
        nodeID,
        payload.error
          ? `\r\n[Terminal exited: ${payload.error}]\r\n`
          : "\r\n[Terminal exited]\r\n",
      )
      setNodes((current) =>
        current.map((node) => {
          if (node.type !== "terminal" || node.id !== nodeID) {
            return node
          }
          return {
            ...node,
            sessionId: undefined,
            status: payload.error ? "error" : "exited",
            error: payload.error || undefined,
          }
        }),
      )
    })
    const offEditorExit = EventsOn("seizen:editor-exit", (payload: unknown) => {
      if (!isEditorExit(payload)) return
      const nodeID = editorSessionNodesRef.current.get(payload.sessionId)
      if (!nodeID) {
        pendingEditorExitsRef.current.set(payload.sessionId, payload)
        if (pendingEditorExitsRef.current.size > 128) {
          const oldest = pendingEditorExitsRef.current.keys().next().value
          if (oldest) pendingEditorExitsRef.current.delete(oldest)
        }
        return
      }
      editorSessionNodesRef.current.delete(payload.sessionId)
      setNodes((current) =>
        current.map((node) =>
          node.id === nodeID && node.type === "editor"
            ? {
                ...node,
                sessionId: undefined,
                url: undefined,
                status: "error",
                error: editorExitMessage(
                  payload,
                  editorTitles[node.editorId] ?? node.editorId,
                ),
              }
            : node,
        ),
      )
    })

    return () => {
      offOutput()
      offExit()
      offEditorExit()
      pendingTerminalsRef.current.clear()
      pendingEditorExitsRef.current.clear()
      terminalViewsRef.current.clear()
      bufferedTerminalOutputRef.current.clear()
      closedTerminalNodesRef.current.clear()
      terminalFocusRequestsRef.current.clear()
    }
  }, [])

  const bufferPendingTerminal = (
    sessionId: string,
    data: string,
    exitError: string | undefined,
  ) => {
    const current = pendingTerminalsRef.current.get(sessionId) ?? {
      output: "",
      exited: false,
      error: "",
      truncated: false,
    }
    const combined = current.output + data
    pendingTerminalsRef.current.set(sessionId, {
      output: combined.slice(-maximumTerminalCharacters),
      exited: current.exited || exitError !== undefined,
      error: exitError ?? current.error,
      truncated: current.truncated || combined.length > maximumTerminalCharacters,
    })
    if (pendingTerminalsRef.current.size > 128) {
      const oldest = pendingTerminalsRef.current.keys().next().value
      if (oldest) pendingTerminalsRef.current.delete(oldest)
    }
  }

  const loadWorkspacePhoto = (
    nodeID: string,
    assetID: string,
    generation = workspaceGeneration.current,
  ) => {
    void projectService
      .getProjectWorkspacePhoto(project, assetID)
      .then((dataURL) => {
        if (generation !== workspaceGeneration.current) return
        setNodes((current) =>
          current.map((node) =>
            node.id === nodeID && node.type === "photo"
              ? { ...node, dataURL, status: "ready", error: undefined }
              : node,
          ),
        )
      })
      .catch((error: unknown) => {
        if (generation !== workspaceGeneration.current) return
        const message = errorMessage(error)
        setNodes((current) =>
          current.map((node) =>
            node.id === nodeID && node.type === "photo"
              ? { ...node, dataURL: undefined, status: "error", error: message }
              : node,
          ),
        )
        setNotice({ tone: "error", message })
      })
  }

  useEffect(() => {
    const generation = ++workspaceGeneration.current
    setLoaded(false)
    setNodes([])
    setRegions([])
    setWorkspaceBackground("")
    setIsPanning(false)
    setSelectedID(null)
    setMaximizedID(null)
    setNotice(null)
    terminalViewsRef.current.clear()
    bufferedTerminalOutputRef.current.clear()
    closedTerminalNodesRef.current.clear()
    terminalFocusRequestsRef.current.clear()
    editorSessionNodesRef.current.clear()
    pendingEditorExitsRef.current.clear()
    historyRef.current = []
    futureRef.current = []

    const load = async () => {
      try {
        const [raw, background] = await Promise.all([
          projectService.getProjectWorkspace(project, activeContext.experimentId),
          projectService
            .getProjectWorkspaceBackground(project)
            .catch(() => ""),
        ])
        if (generation !== workspaceGeneration.current) return
        const layout = parseWorkspace(raw)
        const restored: WorkspaceNode[] = layout.nodes.map((node) => {
          if (node.type === "terminal") return { ...node, status: "starting" }
          if (node.type === "editor") return { ...node, status: "starting" }
          if (node.type === "photo") return { ...node, status: "loading" }
          return node
        })
        const normalized = serializeWorkspace(
          layout.viewport,
          restored,
          layout.regions,
        )
        latestLayoutRef.current = normalized
        lastQueuedLayoutRef.current = normalized
        setViewport(layout.viewport)
        setNodes(restored)
        setRegions(layout.regions)
        setWorkspaceBackground(background)
        setLoaded(true)

        for (const node of restored) {
          if (node.type === "terminal") {
            launchTerminal(node.id, node.shell, generation)
          } else if (node.type === "editor") {
            launchEditor(node.id, node.editorId, generation)
          } else if (node.type === "photo") {
            loadWorkspacePhoto(node.id, node.assetId, generation)
          }
        }
      } catch (error) {
        if (generation !== workspaceGeneration.current) return
        const empty = serializeWorkspace({ x: 80, y: 88, zoom: 1 }, [])
        latestLayoutRef.current = empty
        lastQueuedLayoutRef.current = empty
        setViewport({ x: 80, y: 88, zoom: 1 })
        setNodes([])
        setLoaded(true)
        setNotice({ tone: "error", message: errorMessage(error) })
      }
    }

    void load()
    return () => {
      workspaceGeneration.current += 1
      const prepared = preparedEditorRef.current
      if (prepared?.generation === generation) {
        preparedEditorRef.current = null
        void prepared.promise
          .then((session) => StopProjectEditor(session.sessionId))
          .catch(() => undefined)
      }
      const sessionIDs = new Set<string>()
      for (const [sessionID] of sessionNodesRef.current) {
        sessionIDs.add(sessionID)
      }
      sessionNodesRef.current.clear()
      for (const node of nodesRef.current) {
        if (node.type === "terminal" && node.sessionId) {
          sessionIDs.add(node.sessionId)
        }
      }
      for (const sessionID of sessionIDs) {
        void projectService
          .stopProjectTerminal(sessionID)
          .catch(() => undefined)
          .finally(() => pendingTerminalsRef.current.delete(sessionID))
      }
      const editorSessionIDs = new Set(editorSessionNodesRef.current.keys())
      editorSessionNodesRef.current.clear()
      for (const node of nodesRef.current) {
        if (node.type === "editor" && node.sessionId) {
          editorSessionIDs.add(node.sessionId)
        }
      }
      for (const sessionID of editorSessionIDs) {
        void StopProjectEditor(sessionID).catch(() => undefined)
      }
    }
  }, [activeContext.experimentId, project.id, project.path])

  useEffect(() => {
    if (!loaded) return

    window.clearTimeout(saveTimerRef.current)
    saveTimerRef.current = window.setTimeout(() => {
      if (serializedLayout === lastQueuedLayoutRef.current) return
      lastQueuedLayoutRef.current = serializedLayout
      queueSave(project, serializedLayout, activeContext.experimentId)
    }, 500)

    return () => window.clearTimeout(saveTimerRef.current)
  }, [activeContext.experimentId, loaded, project.id, project.path, serializedLayout])

  useEffect(
    () => () => {
      window.clearTimeout(saveTimerRef.current)
      const latest = latestLayoutRef.current
      if (
        loadedRef.current &&
        latest &&
        latest !== lastQueuedLayoutRef.current
      ) {
        lastQueuedLayoutRef.current = latest
        queueSave(project, latest, activeContext.experimentId)
      }
    },
    [activeContext.experimentId, project.id, project.path],
  )

  const suspendWorkspace = () => {
    if (suspendPromiseRef.current) return suspendPromiseRef.current

    const suspend = (async () => {
      window.clearTimeout(saveTimerRef.current)
      workspaceGeneration.current += 1

      await stopProjectInOrder(
        async () => {
          await saveQueueRef.current
          if (loadedRef.current && latestLayoutRef.current) {
            const latest = latestLayoutRef.current
            await projectService.saveProjectWorkspace(
              project,
              latest,
              activeContext.experimentId,
            )
            lastQueuedLayoutRef.current = latest
          }
        },
        async () => {
          // Cancel the editor first if it's still waiting on readiness.
          await projectService.cleanupProjectRuntime(project.id)
          await Promise.allSettled([
            ...terminalStartPromisesRef.current,
            ...editorStartPromisesRef.current,
          ])
          // Covers a process that finished starting during the first cleanup.
          await projectService.cleanupProjectRuntime(project.id)
          await projectService.cleanupProjectServers(project.id)
        },
        async () => {
          const sessionIDs = new Set(sessionNodesRef.current.keys())
          const editorSessionIDs = new Set(editorSessionNodesRef.current.keys())
          for (const node of nodesRef.current) {
            if (node.type === "terminal" && node.sessionId) {
              sessionIDs.add(node.sessionId)
            } else if (node.type === "editor" && node.sessionId) {
              editorSessionIDs.add(node.sessionId)
            }
          }
          sessionNodesRef.current.clear()
          editorSessionNodesRef.current.clear()
          await Promise.allSettled(
            [
              ...[...sessionIDs].map((sessionID) =>
                projectService
                  .stopProjectTerminal(sessionID)
                  .finally(() => pendingTerminalsRef.current.delete(sessionID)),
              ),
              ...[...editorSessionIDs].map((sessionID) =>
                StopProjectEditor(sessionID),
              ),
            ],
          )
        },
      )
    })().catch((error: unknown) => {
      if (mountedRef.current) {
        setNotice({ tone: "error", message: errorMessage(error) })
      }
      throw error
    })

    suspendPromiseRef.current = suspend
    void suspend.then(
      () => {
        if (suspendPromiseRef.current === suspend) suspendPromiseRef.current = null
      },
      () => {
        if (suspendPromiseRef.current === suspend) suspendPromiseRef.current = null
      },
    )
    return suspend
  }

  useEffect(() => {
    const suspend = () => suspendWorkspace()
    return registerWorkspaceSuspender(project.id, suspend)
  }, [activeContext.experimentId, project.id, project.path])

  const leaveWorkspace = (action: () => void | Promise<void>) => async () => {
    try {
      await suspendWorkspace()
      await action()
    } catch {
      // The workspace stays open and suspendWorkspace already reported the error.
    }
  }

  useEffect(() => {
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key !== "Escape") return
      if (selectedID) setSelectedID(null)
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [selectedID])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 3600)
    return () => window.clearTimeout(timer)
  }, [notice])

  useEffect(() => {
    if (!workspaceMenu) return
    workspaceMenuRef.current?.focus()

    const closeOutside = (event: PointerEvent) => {
      if (!workspaceMenuRef.current?.contains(event.target as Node)) {
        setWorkspaceMenu(null)
      }
    }
    const closeWithEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") setWorkspaceMenu(null)
    }
    document.addEventListener("pointerdown", closeOutside)
    window.addEventListener("keydown", closeWithEscape)
    return () => {
      document.removeEventListener("pointerdown", closeOutside)
      window.removeEventListener("keydown", closeWithEscape)
    }
  }, [workspaceMenu])

  const bringToFront = (id: string) => {
    setSelectedID(id)
    setNodes((current) => {
      const maximumZ = Math.max(0, ...current.map((node) => node.z))
      const selected = current.find((node) => node.id === id)
      if (!selected || selected.z === maximumZ) return current
      return current.map((node) =>
        node.id === id ? { ...node, z: maximumZ + 1 } : node,
      )
    })
  }

  // AppView only mounts in App mode; the automatic jump once the preview is
  // ready is listened for here so that view isn't kept alive in the background.
  useEffect(
    () =>
      EventsOn("app.preview.ready", (payload: unknown) => {
        if (isRecord(payload)) {
          const payloadProjectId = payload.projectId ?? payload.project_id
          if (
            typeof payloadProjectId === "string" &&
            payloadProjectId !== project.id
          ) {
            return
          }
        }
        setProjectMode("app")
      }),
    [project.id],
  )

  // ponytail: in this WebView the menu lands offset from left/top
  // due to a coordinate mismatch that doesn't come from any visible transform;
  // instead of chasing it, we measure where it landed and correct the difference.
  useEffect(() => {
    if (!workspaceMenu) return
    const element = workspaceMenuRef.current
    const canvas = canvasRef.current
    if (!element || !canvas) return
    const frame = requestAnimationFrame(() => {
      const canvasRect = canvas.getBoundingClientRect()
      const rect = element.getBoundingClientRect()
      const deltaX = canvasRect.left + workspaceMenu.x - rect.left
      const deltaY = canvasRect.top + workspaceMenu.y - rect.top
      if (Math.abs(deltaX) > 2 || Math.abs(deltaY) > 2) {
        element.style.left = `${workspaceMenu.x + deltaX}px`
        element.style.top = `${workspaceMenu.y + deltaY}px`
      }
    })
    return () => cancelAnimationFrame(frame)
  }, [workspaceMenu])

  const updateNode = (
    id: string,
    update: (node: WorkspaceNode) => WorkspaceNode,
  ) => {
    setNodes((current) =>
      current.map((node) => (node.id === id ? update(node) : node)),
    )
  }

  // Undo/redo covers panel geometry only (move/resize/z). Snapshots reconcile by
  // id so live session state (terminals, editors) is never rolled back; add/close
  // are history barriers because closing kills real backend sessions.
  const pushGeometryHistory = () => {
    const snapshot = snapshotGeometry(nodesRef.current)
    historyRef.current =
      historyRef.current.length >= maximumHistoryEntries
        ? [...historyRef.current.slice(1), snapshot]
        : [...historyRef.current, snapshot]
    futureRef.current = []
  }

  const clearGeometryHistory = () => {
    historyRef.current = []
    futureRef.current = []
  }

  const applyGeometry = (snapshot: GeometrySnapshot) => {
    setNodes((current) =>
      current.map((node) => {
        const geometry = snapshot.get(node.id)
        return geometry ? { ...node, ...geometry } : node
      }),
    )
  }

  const undoGeometry = () => {
    const previous = historyRef.current.at(-1)
    if (!previous) return
    futureRef.current = [...futureRef.current, snapshotGeometry(nodesRef.current)]
    historyRef.current = historyRef.current.slice(0, -1)
    applyGeometry(previous)
  }

  const redoGeometry = () => {
    const next = futureRef.current.at(-1)
    if (!next) return
    historyRef.current = [...historyRef.current, snapshotGeometry(nodesRef.current)]
    futureRef.current = futureRef.current.slice(0, -1)
    applyGeometry(next)
  }

  const centeredPosition = (width: number, height: number) => {
    const rect = canvasRef.current?.getBoundingClientRect()
    const current = viewportRef.current
    const centered = {
      x: ((rect?.width ?? window.innerWidth) / 2 - current.x) / current.zoom -
        width / 2,
      y: ((rect?.height ?? window.innerHeight) / 2 - current.y) / current.zoom -
        height / 2,
    }
    // New panels land on the nearest free slot instead of stacking.
    return findFreePosition(nodesRef.current, centered, { width, height })
  }

  const ensureNodeCapacity = () => {
    if (nodesRef.current.length >= 100) {
      throw new Error("The workspace supports up to 100 panels")
    }
  }

  const addTerminal = (shell: TerminalShell, region?: StoredRegion, task = "") => {
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID(shell)
    const size = { width: 540, height: 330 }
    // A terminal born inside a folder lands in that folder and inherits its cwd.
    const position = region
      ? findFreePosition(
          nodesRef.current.filter((node) => node.regionId === region.id),
          {
            x: region.x + tidyGap,
            y: region.y + tidyGap * 2,
          },
          size,
        )
      : centeredPosition(size.width, size.height)
    const taskHint = task.split("\n")[0].trim().slice(0, 60)
    const node: TerminalNode = {
      id,
      type: "terminal",
      shell,
      ...position,
      ...size,
      z: Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1,
      status: "starting",
      ...(taskHint ? { taskHint } : {}),
      ...(region ? { regionId: region.id } : {}),
    }
    closedTerminalNodesRef.current.delete(id)
    terminalFocusRequestsRef.current.add(id)
    setNodes((current) => [...current, node])
    setSelectedID(id)
    return {
      id,
      session: launchTerminal(
        id,
        shell,
        workspaceGeneration.current,
        region?.cwd ?? "",
        task,
      ),
    }
  }

  const appTerminalSessions = useMemo<AppTerminalSession[]>(
    () => nodes.flatMap((node) => node.type === "terminal" && node.sessionId
      ? [{
          nodeId: node.id,
          sessionId: node.sessionId,
          agent: node.shell,
          name: terminalTitles[node.shell],
          status: node.status,
        }]
      : []),
    [nodes],
  )

  const openTerminalForApp = async (shell: TerminalShell) => {
    const { id, session } = addTerminal(shell)
    const sessionId = await session
    if (!sessionId) throw new Error(`Could not open ${terminalTitles[shell]}`)
    return {
      nodeId: id,
      sessionId,
      agent: shell,
      name: terminalTitles[shell],
      status: "running" as const,
    }
  }

  const focusAppTerminal = (sessionId: string) => {
    const nodeId = sessionNodesRef.current.get(sessionId) ?? nodesRef.current.find(
      (node) => node.type === "terminal" && node.sessionId === sessionId,
    )?.id
    if (!nodeId) {
      setNotice({ tone: "error", message: "The terminal is no longer available" })
      return
    }
    terminalFocusRequestsRef.current.add(nodeId)
    bringToFront(nodeId)
    setProjectMode("workspace")
    window.requestAnimationFrame(() => {
      if (!terminalFocusRequestsRef.current.delete(nodeId)) return
      terminalViewsRef.current.get(nodeId)?.focus()
    })
  }

  const addBrowser = (url: string) => {
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID("browser")
    const size = { width: 680, height: 430 }
    const position = centeredPosition(size.width, size.height)
    const node: BrowserNode = {
      id,
      type: "browser",
      url,
      ...position,
      ...size,
      z: Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1,
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
  }

  const addPlayer = () => {
    const existing = nodesRef.current.find((node) => node.type === "player")
    if (existing) {
      bringToFront(existing.id)
      return
    }
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID("player")
    const size = { width: 420, height: 190 }
    const node: PlayerNode = {
      id,
      type: "player",
      ...centeredPosition(size.width, size.height),
      ...size,
      z: Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1,
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
  }

  const addPhoto = async () => {
    if (photoBusy) return
    setPhotoBusy(true)
    try {
      ensureNodeCapacity()
      const asset = await projectService.chooseProjectWorkspacePhoto(project)
      if (!asset?.assetId || !asset.dataURL) return
      clearGeometryHistory()
      const id = workspaceID("photo")
      const size = { width: 520, height: 360 }
      const node: PhotoNode = {
        id,
        type: "photo",
        assetId: asset.assetId,
        dataURL: asset.dataURL,
        status: "ready",
        ...centeredPosition(size.width, size.height),
        ...size,
        z: Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1,
      }
      setNodes((current) => [...current, node])
      setSelectedID(id)
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    } finally {
      setPhotoBusy(false)
    }
  }

  const nextNodeZ = () =>
    Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1

  const addNote = (text = "") => {
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID("note")
    const size = { width: 320, height: 280 }
    const node: NoteNode = {
      id,
      type: "note",
      text: text.slice(0, maximumNoteCharacters),
      color: "default",
      ...centeredPosition(size.width, size.height),
      ...size,
      z: nextNodeZ(),
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
  }

  const addTodo = (items: string[] = []) => {
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID("todo")
    const size = { width: 320, height: 300 }
    const node: TodoNode = {
      id,
      type: "todo",
      items: items.slice(0, maximumTodoItems).map((text) => ({
        id: workspaceID("item"),
        text: text.slice(0, maximumTodoItemCharacters),
        done: false,
      })),
      ...centeredPosition(size.width, size.height),
      ...size,
      z: nextNodeZ(),
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
  }

  const documentNodeSize = (kind: DocumentNode["kind"]) =>
    kind === "audio"
      ? { width: 420, height: 190 }
      : kind === "image" || kind === "video"
        ? { width: 560, height: 400 }
        : { width: 560, height: 560 }

  const addDocumentNode = (asset: {
    assetId: string
    kind: string
    name: string
  }) => {
    ensureNodeCapacity()
    clearGeometryHistory()
    if (!documentKinds.includes(asset.kind as DocumentNode["kind"])) {
      throw new Error("This file type is not supported")
    }
    const kind = asset.kind as DocumentNode["kind"]
    const id = workspaceID("document")
    const size = documentNodeSize(kind)
    const node: DocumentNode = {
      id,
      type: "document",
      assetId: asset.assetId,
      name: asset.name,
      kind,
      ...centeredPosition(size.width, size.height),
      ...size,
      z: nextNodeZ(),
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
  }

  const addDocument = async () => {
    if (photoBusy) return
    setPhotoBusy(true)
    try {
      ensureNodeCapacity()
      const asset = await projectService.chooseProjectWorkspaceFile(project)
      if (!asset?.assetId) return
      addDocumentNode(asset)
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    } finally {
      setPhotoBusy(false)
    }
  }

  const importDroppedFiles = async (paths: string[]) => {
    let added = 0
    for (const path of paths.slice(0, 12)) {
      try {
        ensureNodeCapacity()
        const asset = await projectService.importProjectWorkspaceAsset(
          project,
          path,
        )
        if (asset?.assetId) {
          addDocumentNode(asset)
          added += 1
        }
      } catch (error) {
        setNotice({ tone: "error", message: errorMessage(error) })
      }
    }
    if (added > 0) {
      setNotice({
        tone: "success",
        message: added === 1 ? "Document added" : `${added} documents added`,
      })
    }
  }

  useEffect(() => {
    // Only the visible workspace claims OS file drops; hidden live workspaces
    // and other modes let the drop fall through.
    const unsubscribe = subscribeToFileDrops((paths) => {
      if (projectMode !== "workspace" || !loadedRef.current) return false
      if (canvasRef.current?.offsetParent === null) return false
      setDroppingFiles(false)
      void importDroppedFiles(paths)
      return true
    })
    return unsubscribe
  }, [projectMode, project.id, activeContext.experimentId])

  useEffect(() => {
    // Native drag events may be swallowed by the Wails drop handler on some
    // platforms; the overlay is a best-effort hint, the drop itself always works.
    let depth = 0
    const isFileDrag = (event: DragEvent) =>
      Boolean(event.dataTransfer?.types.includes("Files"))
    const onDragEnter = (event: DragEvent) => {
      if (!isFileDrag(event)) return
      depth += 1
      setDroppingFiles(true)
    }
    const onDragLeave = (event: DragEvent) => {
      if (!isFileDrag(event)) return
      depth = Math.max(0, depth - 1)
      if (depth === 0) setDroppingFiles(false)
    }
    const onDropOrEnd = () => {
      depth = 0
      setDroppingFiles(false)
    }
    window.addEventListener("dragenter", onDragEnter)
    window.addEventListener("dragleave", onDragLeave)
    window.addEventListener("drop", onDropOrEnd)
    window.addEventListener("dragend", onDropOrEnd)
    return () => {
      window.removeEventListener("dragenter", onDragEnter)
      window.removeEventListener("dragleave", onDragLeave)
      window.removeEventListener("drop", onDropOrEnd)
      window.removeEventListener("dragend", onDropOrEnd)
    }
  }, [])

  const runQuickAction = (action: WorkspaceQuickAction, shell?: string) => {
    try {
      if (action === "note") addNote()
      else if (action === "todo") addTodo()
      else if (action === "document") void addDocument()
      else if (action === "tidy") tidyAll()
      else
        addTerminal(
          shell && shell in terminalTitles ? (shell as TerminalShell) : "wsl",
        )
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    }
  }
  const runQuickActionRef = useRef(runQuickAction)
  runQuickActionRef.current = runQuickAction

  useEffect(() => {
    // Palette/Home intents: only the visible workspace claims them.
    const onAction = (event: Event) => {
      const detail = (event as CustomEvent).detail as unknown
      if (!isWorkspaceActionDetail(detail)) return
      if (!loadedRef.current || canvasRef.current?.offsetParent === null) return
      detail.claim()
      if (projectMode !== "workspace") setProjectMode("workspace")
      runQuickActionRef.current(detail.action, detail.shell)
    }
    window.addEventListener(workspaceActionEvent, onAction)
    return () => window.removeEventListener(workspaceActionEvent, onAction)
  }, [projectMode])

  useEffect(() => {
    // A Home chip may have queued an action before this workspace existed.
    if (!loaded) return
    const queued = takeQuickAction()
    if (queued && canvasRef.current?.offsetParent !== null) {
      runQuickActionRef.current(queued.action, queued.shell)
    }
  }, [loaded])

  // Agent desk actions: the bridge validated scope and emitted an event; the
  // workspace that owns this project context materializes the panel.
  useEffect(() => {
    const off = EventsOn("seizen:desk-action", (payload: unknown) => {
      if (!isRecord(payload) || payload.projectId !== project.id) return
      if ((payload.experimentId ?? "") !== activeContext.experimentId) return
      if (!loadedRef.current) return
      const detail = payload.payload
      try {
        if (payload.kind === "note" && isRecord(detail) && typeof detail.text === "string") {
          addNote(detail.text)
        } else if (payload.kind === "todo" && isRecord(detail) && Array.isArray(detail.items)) {
          addTodo(detail.items.filter((item): item is string => typeof item === "string"))
        } else if (payload.kind === "browser" && isRecord(detail) && typeof detail.url === "string") {
          addBrowser(normalizeBrowserURL(detail.url))
        } else if (
          payload.kind === "document" &&
          isRecord(detail) &&
          typeof detail.assetId === "string" &&
          typeof detail.kind === "string" &&
          typeof detail.name === "string"
        ) {
          addDocumentNode({
            assetId: detail.assetId,
            kind: detail.kind,
            name: detail.name,
          })
        } else if (payload.kind === "tidy") {
          tidyAll()
        }
      } catch (error) {
        setNotice({ tone: "error", message: errorMessage(error) })
      }
    })
    return off
  }, [project.id, activeContext.experimentId])

  // Plain-language strip of what the assistant just did in this project.
  const [assistantActivity, setAssistantActivity] = useState<{
    message: string
    tone: "ok" | "error"
  } | null>(null)
  useEffect(() => {
    const off = EventsOn("agent.audit", (payload: unknown) => {
      if (!isRecord(payload) || payload.projectId !== project.id) return
      const phrase = assistantPhrase(
        typeof payload.toolName === "string" ? payload.toolName : "",
      )
      if (!phrase) return
      const failed = payload.success === false
      setAssistantActivity({
        message: failed ? `${phrase} — it did not work` : phrase,
        tone: failed ? "error" : "ok",
      })
    })
    return off
  }, [project.id])
  useEffect(() => {
    if (!assistantActivity) return
    const timer = window.setTimeout(() => setAssistantActivity(null), 5000)
    return () => window.clearTimeout(timer)
  }, [assistantActivity])

  // Activity border for agent terminals: green while output flows (working),
  // amber when the CLI has gone quiet and likely waits on the user.
  // ponytail: output-recency heuristic; replace with real agent state if a CLI ever exposes it.
  const [agentActivity, setAgentActivity] = useState<Record<string, "working" | "waiting">>({})
  useEffect(() => {
    const timer = window.setInterval(() => {
      const now = Date.now()
      const next: Record<string, "working" | "waiting"> = {}
      for (const node of nodesRef.current) {
        if (node.type !== "terminal" || !node.sessionId || node.status !== "running") continue
        if (node.shell !== "claude" && node.shell !== "codex" && node.shell !== "opencode") continue
        const last = terminalOutputAtRef.current.get(node.id) ?? 0
        next[node.id] = now - last < 6_000 ? "working" : "waiting"
      }
      setAgentActivity((current) => {
        const currentKeys = Object.keys(current)
        if (
          currentKeys.length === Object.keys(next).length &&
          currentKeys.every((key) => current[key] === next[key])
        ) {
          return current
        }
        return next
      })
    }, 3_000)
    return () => window.clearInterval(timer)
  }, [])

  const retryEditor = (node: EditorNode) => {
    updateNode(node.id, (current) =>
      current.type === "editor"
        ? { ...current, status: "starting", error: undefined }
        : current,
    )
    launchEditor(node.id, node.editorId)
  }

  const addEditor = (editor: core.EditorIntegration) => {
    if (!editor.available) {
      setNotice({
        tone: "error",
        message: `${editor.name} is not available on this computer`,
      })
      return
    }
    const existing = nodesRef.current.find((node) => node.type === "editor")
    if (existing) {
      bringToFront(existing.id)
      if (existing.editorId !== editor.id) {
        setNotice({
          tone: "error",
          message: "Close the current editor before opening another",
        })
        return
      }
      if (existing.status === "error") {
        retryEditor(existing)
      }
      return
    }
    ensureNodeCapacity()
    clearGeometryHistory()
    const id = workspaceID(`editor-${editor.id}`)
    // Detached native editors get a small controller card, not a viewport.
    const size = editor.embedded
      ? { width: 860, height: 560 }
      : { width: 380, height: 230 }
    const node: EditorNode = {
      id,
      type: "editor",
      editorId: editor.id,
      ...centeredPosition(size.width, size.height),
      ...size,
      z: Math.max(0, ...nodesRef.current.map((item) => item.z)) + 1,
      status: "starting",
    }
    setNodes((current) => [...current, node])
    setSelectedID(id)
    launchEditor(id, editor.id)
  }

  const closeNode = (id: string) => {
    const node = nodesRef.current.find((item) => item.id === id)
    if (node?.type === "terminal") {
      closedTerminalNodesRef.current.add(id)
      terminalFocusRequestsRef.current.delete(id)
      const sessionId =
        node.sessionId ??
        [...sessionNodesRef.current].find(([, nodeID]) => nodeID === id)?.[0]
      if (sessionId) {
        void projectService
          .stopProjectTerminal(sessionId)
          .catch((error: unknown) =>
            setNotice({ tone: "error", message: errorMessage(error) }),
          )
      } else if (node.status === "starting") {
        cancelledTerminalStartsRef.current.add(
          `${workspaceGeneration.current}:${id}`,
        )
      }
    } else if (node?.type === "editor") {
      const sessionId =
        node.sessionId ??
        [...editorSessionNodesRef.current].find(([, nodeID]) => nodeID === id)?.[0]
      if (sessionId) {
        editorSessionNodesRef.current.delete(sessionId)
        void StopProjectEditor(sessionId).catch((error: unknown) =>
          setNotice({ tone: "error", message: errorMessage(error) }),
        )
      } else if (node.status === "starting") {
        cancelledEditorStartsRef.current.add(
          `${workspaceGeneration.current}:${id}`,
        )
      }
    } else if (node?.type === "photo" || node?.type === "document") {
      const remaining = nodesRef.current.filter((item) => item.id !== id)
      const layout = serializeWorkspace(
        viewportRef.current,
        remaining,
        regionsRef.current,
      )
      window.clearTimeout(saveTimerRef.current)
      latestLayoutRef.current = layout
      lastQueuedLayoutRef.current = layout
      queueSave(project, layout, activeContext.experimentId, () =>
        node.type === "photo"
          ? projectService.deleteProjectWorkspacePhoto(project, node.assetId)
          : projectService.deleteProjectWorkspaceAsset(project, node.assetId),
      )
    }
    clearGeometryHistory()
    terminalViewsRef.current.delete(id)
    bufferedTerminalOutputRef.current.delete(id)
    terminalOutputAtRef.current.delete(id)
    setNodes((current) => current.filter((item) => item.id !== id))
    setSelectedID((current) => (current === id ? null : current))
    setMaximizedID((current) => (current === id ? null : current))
  }

  const runCommand = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!loaded) {
      setCommandMessage("Wait for the workspace to finish loading")
      return
    }
    const value = command.trim()
    if (!value) return
    const [name, ...parts] = value.split(/\s+/)
    const argument = parts.join(" ")
    const commandName = name.toLocaleLowerCase()
    setCommandMessage("")
    setShowHelp(false)

    try {
      if (commandName === "cmd") {
        addTerminal("cmd")
      } else if (commandName === "wsl") {
        addTerminal("wsl")
      } else if (commandName === "codex") {
        addTerminal("codex")
      } else if (commandName === "claude") {
        addTerminal("claude")
      } else if (commandName === "browser" || commandName === "navegador") {
        addBrowser(normalizeBrowserURL(argument))
      } else if (commandName === "note" || commandName === "nota") {
        addNote()
      } else if (
        commandName === "todo" ||
        commandName === "tareas" ||
        commandName === "lista"
      ) {
        addTodo()
      } else if (
        commandName === "open" ||
        commandName === "abrir" ||
        commandName === "doc" ||
        commandName === "documento"
      ) {
        await addDocument()
      } else if (commandName === "tidy" || commandName === "ordenar") {
        tidyAll()
      } else if (commandName === "folder" || commandName === "carpeta") {
        await onOpenFolder()
        setNotice({ tone: "success", message: "Folder opened" })
      } else if (commandName === "help" || commandName === "ayuda") {
        setShowHelp(true)
        setCommandMessage("Available commands")
        return
      } else {
        // Anything that isn't a shortcut is a conversation: the assistant
        // reads the project and acts on the board.
        await runWorkspaceAssistant(value)
        return
      }
      setCommand("")
    } catch (error) {
      setCommandMessage(errorMessage(error))
    }
  }

  const [workspaceChatId, setWorkspaceChatId] = useState("")
  const [aiWorking, setAiWorking] = useState(false)
  const [wsChatOpen, setWsChatOpen] = useState(false)
  const [wsChatSize, setWsChatSize] = useState<ChatSize>("compact")
  const [wsChatMessages, setWsChatMessages] = useState<ChatMessage[]>([])

  // Delegated agents report back: every brief ends by leaving a results note
  // on the board, closing the loop without the user chasing terminals.
  const taskWithReport = (task: string) =>
    task
      ? task +
        "\n\nWhen you finish, use the seizen_desk_add_note tool to leave a short note on the user's board summarizing what you did and found, in the user's language."
      : ""

  const workspaceActionChip = (action: core.AssistantAction): string => {
    const input = (action.input ?? {}) as unknown as Record<string, unknown>
    if (action.name === "open_terminal") {
      const shell = input.shell === "codex" ? "codex" : "claude"
      const task = typeof input.task === "string" ? input.task.split("\n")[0].trim() : ""
      return task ? `${shell}: ${task.slice(0, 40)}` : `${shell} terminal`
    }
    if (action.name === "open_editor") return `Editor: ${String(input.editor ?? "")}`
    if (action.name === "close_panels") {
      const count = Number(input.count) || 0
      return count > 0
        ? `Closed ${count} ${String(input.type ?? "panel")}${count > 1 ? "s" : ""}`
        : `Closed ${String(input.type ?? "panel")}s`
    }
    if (action.name === "add_note") return "Note added"
    if (action.name === "add_todo") return "Checklist added"
    if (action.name === "open_browser") return "Browser opened"
    if (action.name === "tidy") return "Board tidied"
    return action.name
  }

  const executeWorkspaceAssistantAction = (action: core.AssistantAction) => {
    // Wails types json.RawMessage as number[], but it marshals as the real JSON object.
    const input = (action.input ?? {}) as unknown as Record<string, unknown>
    if (action.name === "open_terminal") {
      const shell = input.shell === "codex" ? "codex" : "claude"
      const task = typeof input.task === "string" ? input.task : ""
      addTerminal(shell, undefined, taskWithReport(task))
    } else if (action.name === "add_note" && typeof input.text === "string") {
      addNote(input.text)
    } else if (action.name === "add_todo" && Array.isArray(input.items)) {
      addTodo(input.items.filter((item): item is string => typeof item === "string"))
    } else if (action.name === "open_browser" && typeof input.url === "string") {
      addBrowser(normalizeBrowserURL(input.url))
    } else if (action.name === "open_editor" && typeof input.editor === "string") {
      const wanted = input.editor.toLowerCase()
      const integration = editorIntegrations.find(
        (editor) => editor.id === wanted || editor.name.toLowerCase() === wanted,
      )
      if (integration) {
        addEditor(integration)
      } else {
        setNotice({ tone: "error", message: `${input.editor} is not installed here` })
      }
    } else if (action.name === "close_panels") {
      const type = typeof input.type === "string" ? input.type : "terminal"
      const shell = typeof input.shell === "string" ? input.shell : ""
      const matches = nodesRef.current.filter((node) => {
        if (type === "all") return true
        if (node.type !== type) return false
        if (node.type === "terminal" && shell) {
          if (shell === "ai") {
            return node.shell === "claude" || node.shell === "codex" || node.shell === "opencode"
          }
          return node.shell === shell
        }
        return true
      })
      const count = Math.min(Number(input.count) || matches.length, matches.length)
      // Newest first: "close 4 terminals" retires the most recent ones.
      for (const node of matches.slice(-count)) closeNode(node.id)
    } else if (action.name === "tidy") {
      tidyAll()
    }
  }

  // One line the model can read: what is on the board right now.
  const boardSummary = () => {
    const parts = nodesRef.current.map((node) =>
      node.type === "terminal"
        ? `terminal(${node.shell}${node.taskHint ? `: ${node.taskHint}` : ""})`
        : node.type,
    )
    return parts.join(", ") || "(empty)"
  }

  const runWorkspaceAssistant = async (prompt: string) => {
    if (aiWorking) return
    setAiWorking(true)
    setWsChatOpen(true)
    setWsChatMessages((messages) => [...messages, { role: "user", content: prompt }])
    setCommand("")
    document.documentElement.dataset.aiActive = "on"
    try {
      const reply = await AskWorkspaceAssistant(
        project.id,
        workspaceChatId,
        prompt,
        boardSummary(),
      )
      setWorkspaceChatId(reply.chatId)
      const chips = (reply.actions ?? []).map(workspaceActionChip)
      if (reply.text || chips.length > 0) {
        setWsChatMessages((messages) => [
          ...messages,
          { role: "assistant", content: reply.text || "Done.", chips },
        ])
      }
      for (const action of reply.actions ?? []) {
        executeWorkspaceAssistantAction(action)
        await new Promise((resolve) => setTimeout(resolve, 400))
      }
    } catch (error) {
      setWsChatMessages((messages) => [
        ...messages,
        { role: "assistant", content: errorMessage(error), error: true },
      ])
    } finally {
      delete document.documentElement.dataset.aiActive
      setAiWorking(false)
    }
  }

  const beginMove = (
    event: ReactPointerEvent<HTMLElement>,
    node: WorkspaceNode,
  ) => {
    if (event.button !== 0) return
    if ((event.target as HTMLElement).closest("button,input")) return
    event.stopPropagation()
    event.currentTarget.setPointerCapture(event.pointerId)
    pushGeometryHistory()
    bringToFront(node.id)
    interactionRef.current = {
      kind: "move",
      pointerId: event.pointerId,
      id: node.id,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startX: node.x,
      startY: node.y,
      zoom: viewportRef.current.zoom,
    }
    // Wobbly windows bend around the grab point, so the grabbed spot follows
    // the pointer and the rest of the panel trails behind.
    const element = panelElsRef.current.get(node.id)
    if (element) {
      const rect = element.getBoundingClientRect()
      if (rect.width > 0 && rect.height > 0) {
        grabWobble(
          node.id,
          node.x,
          node.y,
          (event.clientX - rect.left) / rect.width,
          (event.clientY - rect.top) / rect.height,
        )
      }
    }
  }

  const beginResize = (
    event: ReactPointerEvent<HTMLButtonElement>,
    node: WorkspaceNode,
  ) => {
    if (event.button !== 0) return
    event.stopPropagation()
    event.currentTarget.setPointerCapture(event.pointerId)
    pushGeometryHistory()
    bringToFront(node.id)
    interactionRef.current = {
      kind: "resize",
      pointerId: event.pointerId,
      id: node.id,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startWidth: node.width,
      startHeight: node.height,
      zoom: viewportRef.current.zoom,
    }
  }

  const onCanvasPointerDown = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (event.button !== 0 || event.target !== event.currentTarget) return
    setWorkspaceMenu(null)
    event.currentTarget.setPointerCapture(event.pointerId)
    setSelectedID(null)
    setIsPanning(true)
    const base = viewportOverrideRef.current ?? viewportRef.current
    interactionRef.current = {
      kind: "pan",
      pointerId: event.pointerId,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startX: base.x,
      startY: base.y,
    }
  }

  const openWorkspaceMenu = (event: ReactMouseEvent<HTMLDivElement>) => {
    if (!loaded || event.target !== event.currentTarget) return
    event.preventDefault()
    // Editors enabled in Resources without closing the project appear when the menu opens.
    void GetEditorIntegrations()
      .then((integrations) => setEditorIntegrations(integrations))
      .catch(() => undefined)
    const rect = event.currentTarget.getBoundingClientRect()
    setSelectedID(null)
    setWorkspaceMenu({
      x: Math.max(8, Math.min(event.clientX - rect.left, rect.width - 232)),
      y: Math.max(8, Math.min(event.clientY - rect.top, rect.height - 152)),
      submenuSide:
        event.clientX - rect.left > rect.width - 456 ? "left" : "right",
    })
  }

  // Direct DOM writes per frame; React doesn't re-render until release.
  const onCanvasPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const interaction = interactionRef.current
    if (!interaction || interaction.pointerId !== event.pointerId) return
    const deltaX = event.clientX - interaction.startClientX
    const deltaY = event.clientY - interaction.startClientY

    if (interaction.kind === "pan") {
      const next = {
        x: interaction.startX + deltaX,
        y: interaction.startY + deltaY,
        zoom: viewportRef.current.zoom,
      }
      viewportOverrideRef.current = next
      applyViewportDOM(next)
    } else if (interaction.kind === "move") {
      const x = interaction.startX + deltaX / interaction.zoom
      const y = interaction.startY + deltaY / interaction.zoom
      nodeOverrideRef.current = { id: interaction.id, x, y }
      const element = panelElsRef.current.get(interaction.id)
      if (element) element.style.translate = `${x}px ${y}px`
      wobblePanel(interaction.id, x, y)
    } else if (interaction.kind === "region-move") {
      const worldDeltaX = deltaX / interaction.zoom
      const worldDeltaY = deltaY / interaction.zoom
      const x = interaction.startX + worldDeltaX
      const y = interaction.startY + worldDeltaY
      const members = new Map<string, { x: number; y: number }>()
      for (const member of interaction.members) {
        const position = {
          x: member.startX + worldDeltaX,
          y: member.startY + worldDeltaY,
        }
        members.set(member.id, position)
        const element = panelElsRef.current.get(member.id)
        if (element) {
          element.style.translate = `${position.x}px ${position.y}px`
        }
      }
      regionOverrideRef.current = { id: interaction.id, x, y, members }
      const element = regionElsRef.current.get(interaction.id)
      if (element) element.style.translate = `${x}px ${y}px`
    } else if (interaction.kind === "region-resize") {
      const width = Math.max(160, interaction.startWidth + deltaX / interaction.zoom)
      const height = Math.max(160, interaction.startHeight + deltaY / interaction.zoom)
      regionOverrideRef.current = { id: interaction.id, width, height }
      const element = regionElsRef.current.get(interaction.id)
      if (element) {
        element.style.width = `${width}px`
        element.style.height = `${height}px`
      }
    } else {
      const width = Math.max(
        minimumNodeWidth,
        interaction.startWidth + deltaX / interaction.zoom,
      )
      const height = Math.max(
        minimumNodeHeight,
        interaction.startHeight + deltaY / interaction.zoom,
      )
      nodeOverrideRef.current = { id: interaction.id, width, height }
      const element = panelElsRef.current.get(interaction.id)
      if (element) {
        element.style.width = `${width}px`
        element.style.height = `${height}px`
      }
    }
  }

  const endInteraction = (event: ReactPointerEvent<HTMLDivElement>) => {
    if (interactionRef.current?.pointerId !== event.pointerId) return
    if (interactionRef.current.kind === "move") {
      releaseWobble(interactionRef.current.id)
    }
    interactionRef.current = null
    setIsPanning(false)

    const pendingViewport = viewportOverrideRef.current
    if (pendingViewport) {
      viewportOverrideRef.current = null
      setViewport(pendingViewport)
    }
    const pendingNode = nodeOverrideRef.current
    if (pendingNode) {
      nodeOverrideRef.current = null
      const moved = pendingNode.x !== undefined || pendingNode.y !== undefined
      const neighbors = nodesRef.current.filter(
        (node) => node.id !== pendingNode.id,
      )
      updateNode(pendingNode.id, (node) => {
        const box = {
          x: pendingNode.x ?? node.x,
          y: pendingNode.y ?? node.y,
          width: pendingNode.width ?? node.width,
          height: pendingNode.height ?? node.height,
        }
        // Grid + neighbor-edge snap on release keeps the board tidy for free.
        const snapped = moved ? snapDragPosition(box, neighbors) : box
        const settled = { ...box, x: snapped.x, y: snapped.y }
        // Folder membership follows where the panel actually landed.
        const regionId = regionContaining(settled, regionsRef.current)
        return { ...node, ...settled, regionId }
      })
    }

    const pendingRegion = regionOverrideRef.current
    if (pendingRegion) {
      regionOverrideRef.current = null
      setRegions((current) =>
        current.map((region) =>
          region.id === pendingRegion.id
            ? {
                ...region,
                x: pendingRegion.x ?? region.x,
                y: pendingRegion.y ?? region.y,
                width: pendingRegion.width ?? region.width,
                height: pendingRegion.height ?? region.height,
              }
            : region,
        ),
      )
      const members = pendingRegion.members
      if (members && members.size > 0) {
        setNodes((current) =>
          current.map((node) => {
            const position = members.get(node.id)
            return position ? { ...node, x: position.x, y: position.y } : node
          }),
        )
      }
    }
  }

  const computeZoomViewport = (
    nextZoom: number,
    clientX: number,
    clientY: number,
  ): WorkspaceViewport | null => {
    const rect = canvasRef.current?.getBoundingClientRect()
    if (!rect) return null
    const current = viewportOverrideRef.current ?? viewportRef.current
    const zoom = clampZoom(nextZoom)
    const localX = clientX - rect.left
    const localY = clientY - rect.top
    const worldX = (localX - current.x) / current.zoom
    const worldY = (localY - current.y) / current.zoom
    return { x: localX - worldX * zoom, y: localY - worldY * zoom, zoom }
  }

  const zoomAt = (nextZoom: number, clientX: number, clientY: number) => {
    const next = computeZoomViewport(nextZoom, clientX, clientY)
    if (!next) return
    window.clearTimeout(wheelCommitTimerRef.current)
    viewportOverrideRef.current = null
    setViewport(next)
  }

  const zoomFromCenter = (nextZoom: number) => {
    const rect = canvasRef.current?.getBoundingClientRect()
    if (!rect) return
    zoomAt(nextZoom, rect.left + rect.width / 2, rect.top + rect.height / 2)
  }

  const zoomToNodes = (targets: readonly WorkspaceNode[]) => {
    const rect = canvasRef.current?.getBoundingClientRect()
    if (!rect) return
    setViewport(fitWorkspaceViewport(rect.width, rect.height, targets))
  }

  // --- Canvas folders (regions) ------------------------------------------
  const updateRegion = (
    id: string,
    update: (region: StoredRegion) => StoredRegion,
  ) => {
    setRegions((current) =>
      current.map((region) => (region.id === id ? update(region) : region)),
    )
  }

  const renameRegion = async (region: StoredRegion) => {
    const label = await promptDialog({
      title: "Rename folder",
      placeholder: "Folder name",
      initial: region.label,
    })
    if (label === null) return
    const trimmed = label.trim().slice(0, maximumRegionLabel)
    if (trimmed) updateRegion(region.id, (current) => ({ ...current, label: trimmed }))
  }

  const setRegionFolder = async (region: StoredRegion) => {
    const value = await promptDialog({
      title: "Folder location",
      message:
        "Subfolder of the project (relative). New terminals in this folder start there. Leave empty for the project root.",
      placeholder: "for example: docs/contracts",
      initial: region.cwd ?? "",
    })
    if (value === null) return
    const cwd = value.trim().replace(/\\/g, "/")
    if (cwd.includes("..") || cwd.includes(":")) {
      setNotice({ tone: "error", message: "The folder must stay inside the project" })
      return
    }
    updateRegion(region.id, (current) => ({
      ...current,
      cwd: cwd || undefined,
    }))
  }

  const dissolveRegion = async (region: StoredRegion) => {
    const accepted = await confirmDialog({
      title: "Dissolve folder",
      message: `The panels inside "${region.label}" stay on the board; only the folder outline goes away.`,
      confirmLabel: "Dissolve",
    })
    if (!accepted) return
    clearGeometryHistory()
    setRegions((current) => current.filter((item) => item.id !== region.id))
    setNodes((current) =>
      current.map((node) =>
        node.regionId === region.id ? { ...node, regionId: undefined } : node,
      ),
    )
  }

  const beginRegionMove = (
    event: ReactPointerEvent<HTMLElement>,
    region: StoredRegion,
  ) => {
    if (event.button !== 0) return
    if ((event.target as HTMLElement).closest("button,input")) return
    event.stopPropagation()
    event.currentTarget.setPointerCapture(event.pointerId)
    clearGeometryHistory()
    interactionRef.current = {
      kind: "region-move",
      pointerId: event.pointerId,
      id: region.id,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startX: region.x,
      startY: region.y,
      zoom: viewportRef.current.zoom,
      members: nodesRef.current
        .filter((node) => node.regionId === region.id)
        .map((node) => ({ id: node.id, startX: node.x, startY: node.y })),
    }
  }

  const beginRegionResize = (
    event: ReactPointerEvent<HTMLButtonElement>,
    region: StoredRegion,
  ) => {
    if (event.button !== 0) return
    event.stopPropagation()
    event.currentTarget.setPointerCapture(event.pointerId)
    clearGeometryHistory()
    interactionRef.current = {
      kind: "region-resize",
      pointerId: event.pointerId,
      id: region.id,
      startClientX: event.clientX,
      startClientY: event.clientY,
      startWidth: region.width,
      startHeight: region.height,
      zoom: viewportRef.current.zoom,
    }
  }

  // One-click masonry over the whole board; undoable like any other move.
  const tidyAll = () => {
    if (nodesRef.current.length === 0) return
    pushGeometryHistory()
    const rect = canvasRef.current?.getBoundingClientRect()
    const canvasWidth =
      (rect?.width ?? window.innerWidth) / viewportRef.current.zoom
    const arranged = tidyLayout(nodesRef.current, canvasWidth)
    setNodes((current) =>
      current.map((node, index) => {
        const position = arranged.get(index)
        return position ? { ...node, x: position.x, y: position.y } : node
      }),
    )
    window.requestAnimationFrame(() => zoomToNodes(nodesRef.current))
  }

  const closeZoomMenu = () => zoomDetailsRef.current?.removeAttribute("open")
  const runZoomAction = (action: () => void) => () => {
    closeZoomMenu()
    action()
  }

  const closeBackgroundMenu = () =>
    backgroundDetailsRef.current?.removeAttribute("open")
  const runBackgroundAction = (action: () => Promise<void>) => () => {
    closeBackgroundMenu()
    void action()
  }

  const chooseWorkspaceBackground = async () => {
    if (backgroundBusy) return
    setBackgroundBusy(true)
    try {
      const background =
        await projectService.chooseProjectWorkspaceBackground(project)
      if (!background) return
      setWorkspaceBackground(background)
      setNotice({ tone: "success", message: "Workspace background updated" })
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    } finally {
      setBackgroundBusy(false)
    }
  }

  const clearWorkspaceBackground = async () => {
    if (backgroundBusy) return
    setBackgroundBusy(true)
    try {
      await projectService.clearProjectWorkspaceBackground(project)
      setWorkspaceBackground("")
      setNotice({ tone: "success", message: "Default background restored" })
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    } finally {
      setBackgroundBusy(false)
    }
  }

  const closeProjectMenu = () => projectMenuRef.current?.removeAttribute("open")
  const runProjectMenuAction = (action: () => void | Promise<void>) => () => {
    closeProjectMenu()
    void action()
  }

  const runWorkspaceMenuAction = (action: () => void, group?: string) => () => {
    setWorkspaceMenu(null)
    if (group) recordMenuGroupUse(group)
    try {
      action()
      setCommandMessage("")
      setShowHelp(false)
    } catch (error) {
      setCommandMessage(errorMessage(error))
    }
  }

  // Wheel zoom writes the DOM directly and commits 160ms after the
  // last tick; that way a burst of wheel events doesn't re-render the tree per event.
  const onCanvasWheel = (event: ReactWheelEvent<HTMLDivElement>) => {
    if (event.target !== event.currentTarget) return
    event.preventDefault()
    const current = viewportOverrideRef.current ?? viewportRef.current
    const next = computeZoomViewport(
      current.zoom * Math.exp(-event.deltaY * 0.001),
      event.clientX,
      event.clientY,
    )
    if (!next) return
    viewportOverrideRef.current = next
    applyViewportDOM(next)
    window.clearTimeout(wheelCommitTimerRef.current)
    wheelCommitTimerRef.current = window.setTimeout(() => {
      const pending = viewportOverrideRef.current
      if (pending && !interactionRef.current) {
        viewportOverrideRef.current = null
        setViewport(pending)
      }
    }, 160)
  }

  useEffect(() => {
    const onZoomShortcut = (event: KeyboardEvent) => {
      const target = event.target
      if (
        target instanceof HTMLElement &&
        target.closest("input, textarea, select, [contenteditable='true']")
      ) {
        return
      }

      const command = event.ctrlKey || event.metaKey
      let action: (() => void) | undefined
      if (command && !event.altKey && event.key.toLowerCase() === "z") {
        action = event.shiftKey ? redoGeometry : undoGeometry
      } else if (command && !event.altKey && event.key.toLowerCase() === "y") {
        action = redoGeometry
      } else if (command && (event.key === "+" || event.key === "=")) {
        action = () => zoomFromCenter(viewportRef.current.zoom + 0.1)
      } else if (command && event.key === "-") {
        action = () => zoomFromCenter(viewportRef.current.zoom - 0.1)
      } else if (event.shiftKey && !command && !event.altKey) {
        if (event.code === "Digit0") action = () => zoomFromCenter(1)
        if (event.code === "Digit1") action = () => zoomToNodes(nodesRef.current)
        if (event.code === "Digit2" && selectedID) {
          action = () => {
            const selected = nodesRef.current.find(
              (node) => node.id === selectedID,
            )
            if (selected) zoomToNodes([selected])
          }
        }
        if (event.code === "Digit3") action = tidyAll
      }

      if (!action) return
      event.preventDefault()
      closeZoomMenu()
      action()
    }

    window.addEventListener("keydown", onZoomShortcut)
    return () => window.removeEventListener("keydown", onZoomShortcut)
  }, [selectedID])

  // If a re-render arrives mid-drag (polls, events), we render the
  // pending ref value so the view doesn't jump back to a stale state.
  const viewportView = viewportOverrideRef.current ?? viewport

  return (
    <section
      aria-label={`Workspace for ${project.name}`}
      className="fixed inset-0 z-[100] overflow-hidden bg-[var(--surface)] text-[var(--on-surface)]"
    >
      <header
        onDoubleClick={(event) => {
          if (!(event.target as HTMLElement).closest(".window-no-drag")) {
            WindowToggleMaximise()
          }
        }}
        className="window-drag absolute inset-x-0 top-0 z-[70] flex h-14 items-center justify-between px-3 sm:px-4"
      >
        <div className="pointer-events-auto flex min-w-0 items-center gap-2">
          <details
            ref={projectMenuRef}
            className="window-no-drag relative"
            onBlur={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget)) {
                closeProjectMenu()
              }
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault()
                closeProjectMenu()
                projectMenuSummaryRef.current?.focus()
              }
            }}
          >
            <summary
              ref={projectMenuSummaryRef}
              aria-label="Open project menu"
              title="Project menu"
              className="flex h-8 w-11 cursor-pointer list-none items-center justify-center rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] text-[var(--on-surface-variant)] shadow-[0_3px_10px_var(--shadow-elevated)] outline-none transition-colors hover:bg-[var(--surface-container)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&::-webkit-details-marker]:hidden"
            >
              <Menu className="size-4" strokeWidth={1.7} />
            </summary>

            <div
              role="menu"
              aria-label={`${project.name} menu`}
              className="command-panel absolute left-0 top-full z-50 mt-2 w-60 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 shadow-[0_16px_40px_var(--shadow-elevated)] backdrop-blur-2xl"
            >
              <ProjectMenuAction
                onClick={runProjectMenuAction(onBack)}
              >
                <ArrowLeft className="size-4" />
                <span>Go to all projects</span>
              </ProjectMenuAction>
              <ProjectMenuAction
                onClick={runProjectMenuAction(() => {
                  void onDownload()
                    .then((path) => {
                      if (path) {
                        setNotice({
                          tone: "success",
                          message: "Project downloaded as ZIP",
                        })
                      }
                    })
                    .catch((error: unknown) =>
                      setNotice({ tone: "error", message: errorMessage(error) }),
                    )
                })}
              >
                <Download className="size-4" />
                <span>Download project</span>
              </ProjectMenuAction>

              <div role="separator" className="mx-2 my-1 h-px bg-[var(--outline-variant)]" />

              <ProjectMenuAction
                onClick={runProjectMenuAction(leaveWorkspace(onEdit))}
              >
                <Pencil className="size-4" />
                <span>Edit</span>
              </ProjectMenuAction>
              <ProjectMenuAction
                onClick={runProjectMenuAction(() => {
                  setShowHelp(true)
                  setCommandMessage("Available commands")
                })}
              >
                <CircleHelp className="size-4" />
                <span>Help</span>
              </ProjectMenuAction>
              <ProjectMenuAction
                onClick={runProjectMenuAction(leaveWorkspace(onOpenSettings))}
              >
                <Settings className="size-4" />
                <span>Settings</span>
              </ProjectMenuAction>

              <div role="separator" className="mx-2 my-1 h-px bg-[var(--outline-variant)]" />

              <ProjectMenuAction
                destructive
                title={
                  project.source === "imported"
                    ? "Leaves the library; your folder on disk is untouched"
                    : undefined
                }
                onClick={runProjectMenuAction(leaveWorkspace(onDelete))}
              >
                <Trash2 className="size-4" />
                <span>
                  {project.source === "imported"
                    ? "Remove from Seizen"
                    : "Delete project"}
                </span>
              </ProjectMenuAction>
              <ProjectMenuAction
                shortcut="Ctrl+K"
                onClick={runProjectMenuAction(onOpenCommandMenu)}
              >
                <CommandIcon className="size-4" />
                <span>Command menu</span>
              </ProjectMenuAction>
            </div>
          </details>
          <div className={cn("min-w-0", maximizedID && "hidden")}>
            <p className="truncate text-sm font-semibold tracking-[-0.02em]">
              {project.name}
            </p>
            <p className="truncate text-[0.64rem] text-[var(--on-surface-variant)]">
              {projectMode === "workspace"
                ? "Workspace"
                : projectMode === "app"
                  ? "App"
                  : "Server Lab"}
              {activeContext.experimentId ? ` · ${activeContext.name}` : ""}
            </p>
          </div>
        </div>
        <div className="window-no-drag pointer-events-auto absolute left-1/2 top-2.5 -translate-x-1/2">
          <ProjectModeSelector value={projectMode} onChange={setProjectMode} />
        </div>
        <WindowControls className="pointer-events-auto" />
      </header>

      <div
        ref={canvasRef}
        aria-label="Workspace canvas"
        onPointerDown={onCanvasPointerDown}
        onPointerMove={onCanvasPointerMove}
        onPointerUp={endInteraction}
        onPointerCancel={endInteraction}
        onLostPointerCapture={endInteraction}
        onWheel={onCanvasWheel}
        onContextMenu={openWorkspaceMenu}
        onScroll={(event) => {
          // Focusing a panel below the fold makes the browser auto-scroll this
          // overflow-hidden container, shearing the background layers. Panning
          // is transform-based, so native scroll must always stay at 0.
          event.currentTarget.scrollTop = 0
          event.currentTarget.scrollLeft = 0
        }}
        className={cn(
          "absolute inset-0 cursor-grab overflow-hidden outline-none active:cursor-grabbing",
          projectMode !== "workspace" && "invisible pointer-events-none",
        )}
        style={{
          touchAction: "none",
        }}
      >
        {workspaceBackground &&
          (workspaceBackground.startsWith("data:") ? (
            <div
              aria-hidden="true"
              data-workspace-layer="image"
              className="pointer-events-none absolute inset-0 bg-cover bg-center bg-no-repeat"
              style={{
                backgroundImage: `url(${JSON.stringify(workspaceBackground)})`,
              }}
            />
          ) : (
            <video
              aria-hidden="true"
              data-workspace-layer="video"
              className="pointer-events-none absolute inset-0 size-full object-cover"
              src={workspaceBackground}
              autoPlay
              loop
              muted
              playsInline
            />
          ))}
        <div
          ref={gridFineRef}
          aria-hidden="true"
          data-workspace-layer="grid-fine"
          className="pointer-events-none absolute inset-0 transition-opacity duration-150"
          style={{
            backgroundImage:
              "radial-gradient(circle, var(--dot) 1px, transparent 1.15px)",
            backgroundPosition: `${viewportView.x}px ${viewportView.y}px`,
            backgroundSize: `${20 * viewportView.zoom}px ${20 * viewportView.zoom}px`,
            opacity: workspaceBackground
              ? isPanning
                ? 0.78
                : 0.5
              : isPanning
                ? 1
                : 0.82,
          }}
        />
        <div
          ref={gridMajorRef}
          aria-hidden="true"
          data-workspace-layer="grid-major"
          className="pointer-events-none absolute inset-0 transition-opacity duration-150"
          style={{
            backgroundImage:
              "radial-gradient(circle, var(--dot) 1.8px, transparent 2px)",
            backgroundPosition: `${viewportView.x}px ${viewportView.y}px`,
            backgroundSize: `${100 * viewportView.zoom}px ${100 * viewportView.zoom}px`,
            opacity: workspaceBackground
              ? isPanning
                ? 0.68
                : 0.38
              : isPanning
                ? 0.82
                : 0.55,
          }}
        />
        <div
          ref={panelsLayerRef}
          aria-label="Workspace panels"
          data-workspace-layer="panels"
          className="pointer-events-none absolute inset-0 origin-top-left"
          style={{
            transform: maximizedID
              ? "none"
              : `translate3d(${viewportView.x}px, ${viewportView.y}px, 0) scale(${viewportView.zoom})`,
          }}
        >
          {regions.map((region) => (
            <RegionBox
              key={region.id}
              region={
                regionOverrideRef.current?.id === region.id
                  ? {
                      ...region,
                      x: regionOverrideRef.current.x ?? region.x,
                      y: regionOverrideRef.current.y ?? region.y,
                      width: regionOverrideRef.current.width ?? region.width,
                      height: regionOverrideRef.current.height ?? region.height,
                    }
                  : region
              }
              hidden={maximizedID !== null}
              memberCount={nodes.filter((node) => node.regionId === region.id).length}
              regionRef={(element) => {
                if (element) regionElsRef.current.set(region.id, element)
                else regionElsRef.current.delete(region.id)
              }}
              onMove={(event) => beginRegionMove(event, region)}
              onResize={(event) => beginRegionResize(event, region)}
              onRename={() => void renameRegion(region)}
              onCycleColor={() =>
                updateRegion(region.id, (current) => ({
                  ...current,
                  color:
                    noteColors[
                      (noteColors.indexOf(current.color) + 1) % noteColors.length
                    ],
                }))
              }
              onSetFolder={() => void setRegionFolder(region)}
              onTerminalHere={() => {
                try {
                  addTerminal("wsl", region)
                } catch (error) {
                  setNotice({ tone: "error", message: errorMessage(error) })
                }
              }}
              onDissolve={() => void dissolveRegion(region)}
            />
          ))}
          {nodes.map((node) => (
            <WorkspacePanel
              key={node.id}
              node={
                nodeOverrideRef.current?.id === node.id
                  ? {
                      ...node,
                      x: nodeOverrideRef.current.x ?? node.x,
                      y: nodeOverrideRef.current.y ?? node.y,
                      width: nodeOverrideRef.current.width ?? node.width,
                      height: nodeOverrideRef.current.height ?? node.height,
                    }
                  : node
              }
              panelRef={(element) => {
                if (element) panelElsRef.current.set(node.id, element)
                else panelElsRef.current.delete(node.id)
              }}
              selected={selectedID === node.id}
              maximized={maximizedID === node.id}
              obscured={maximizedID !== null && maximizedID !== node.id}
              activity={agentActivity[node.id]}
              onSelect={() => bringToFront(node.id)}
              onMove={(event) => beginMove(event, node)}
              onMoveKeyboard={(x, y) =>
                updateNode(node.id, (current) => ({
                  ...current,
                  x: current.x + x,
                  y: current.y + y,
                }))
              }
              onResize={(event) => beginResize(event, node)}
              onResizeKeyboard={(width, height) =>
                updateNode(node.id, (current) => ({
                  ...current,
                  width: Math.max(minimumNodeWidth, current.width + width),
                  height: Math.max(minimumNodeHeight, current.height + height),
                }))
              }
              onToggleMaximize={() =>
                setMaximizedID((current) =>
                  current === node.id ? null : node.id,
                )
              }
              onClose={() => closeNode(node.id)}
            >
              {node.type === "terminal" ? (
                <RealTerminal
                  sessionId={node.sessionId}
                  status={node.status}
                  error={node.error}
                  ariaLabel={`Terminal ${node.shell}`}
                  zoom={maximizedID === node.id ? 1 : viewport.zoom}
                  onReady={(view) => registerTerminalView(node.id, view)}
                  onData={(sessionID, data) =>
                    projectService.writeProjectTerminal(sessionID, data)
                  }
                  onBinary={(sessionID, data) =>
                    projectService.writeProjectTerminalBinary(
                      sessionID,
                      window.btoa(data),
                    )
                  }
                  onResize={(sessionID, columns, rows) =>
                    projectService.resizeProjectTerminal(
                      sessionID,
                      columns,
                      rows,
                    )
                  }
                />
              ) : node.type === "browser" ? (
                <BrowserPanel
                  node={node}
                  onNavigate={(url) =>
                    updateNode(node.id, (current) =>
                      current.type === "browser"
                        ? { ...current, url }
                        : current,
                    )
                  }
                />
              ) : node.type === "player" ? (
                <SpotifyPlayerPanel />
              ) : node.type === "photo" ? (
                <WorkspacePhotoPanel
                  node={node}
                  onRetry={() => {
                    updateNode(node.id, (current) =>
                      current.type === "photo"
                        ? { ...current, status: "loading", error: undefined }
                        : current,
                    )
                    loadWorkspacePhoto(node.id, node.assetId)
                  }}
                  onImageError={() =>
                    updateNode(node.id, (current) =>
                      current.type === "photo"
                        ? {
                            ...current,
                            dataURL: undefined,
                            status: "error",
                            error: "Could not display photo",
                          }
                        : current,
                    )
                  }
                />
              ) : node.type === "note" ? (
                <NotePanel
                  node={node}
                  onChange={(text) =>
                    updateNode(node.id, (current) =>
                      current.type === "note" ? { ...current, text } : current,
                    )
                  }
                  onCycleColor={() =>
                    updateNode(node.id, (current) =>
                      current.type === "note"
                        ? {
                            ...current,
                            color:
                              noteColors[
                                (noteColors.indexOf(current.color) + 1) %
                                  noteColors.length
                              ],
                          }
                        : current,
                    )
                  }
                />
              ) : node.type === "todo" ? (
                <TodoPanel
                  node={node}
                  onChange={(items) =>
                    updateNode(node.id, (current) =>
                      current.type === "todo" ? { ...current, items } : current,
                    )
                  }
                />
              ) : node.type === "document" ? (
                <DocumentPanel
                  url={workspaceAssetURL(project.id, node.assetId)}
                  kind={node.kind}
                  name={node.name}
                />
              ) : (
                <EditorPanel node={node} onRetry={() => retryEditor(node)} />
              )}
            </WorkspacePanel>
          ))}
        </div>

        {droppingFiles && (
          <div
            aria-hidden="true"
            className="overlay-in pointer-events-none absolute inset-0 z-30 flex items-center justify-center bg-[var(--surface)]/60 backdrop-blur-[2px]"
          >
            <div className="flex items-center gap-3 rounded-full border border-[var(--focus-border)] bg-[var(--surface-container-high)] px-6 py-3 shadow-[0_1px_2px_var(--shadow-soft),0_8px_24px_var(--shadow-elevated)] backdrop-blur-xl">
              <FileText className="size-4 text-[var(--primary)]" strokeWidth={1.7} />
              <span className="text-sm font-medium">Drop to add · Suelta para añadir</span>
            </div>
          </div>
        )}

        {!loaded && (
          <div className="overlay-in absolute inset-0 z-20 flex items-center justify-center bg-[var(--surface)]/70 text-xs text-[var(--on-surface-variant)] backdrop-blur-sm">
            <LoaderCircle className="mr-2 size-4 animate-spin" />
            Loading workspace
          </div>
        )}

        {loaded && nodes.length === 0 && !workspaceMenu && (
          <div className="view-enter pointer-events-none absolute inset-0 flex flex-col items-center justify-center gap-4 text-center">
            <span className="pointer-events-none flex size-12 items-center justify-center rounded-2xl bg-[var(--surface-container)] text-[var(--on-surface-variant)] shadow-[0_1px_3px_var(--shadow-soft)]">
              <FileText className="size-5" strokeWidth={1.6} />
            </span>
            <div className="pointer-events-none">
              <p className="text-sm font-semibold text-[var(--on-surface)]">
                This space is empty
              </p>
              <p className="mt-1 max-w-xs text-xs leading-5 text-[var(--on-surface-variant)]">
                Drop a file here, or start with one of these.
              </p>
            </div>
            <div className="pointer-events-auto flex flex-wrap items-center justify-center gap-2 px-6">
              <EmptyCanvasAction icon={FileText} disabled={photoBusy} onClick={() => void addDocument()}>
                Open document
              </EmptyCanvasAction>
              <EmptyCanvasAction icon={StickyNote} onClick={() => addNote()}>
                Note
              </EmptyCanvasAction>
              <EmptyCanvasAction icon={ListChecks} onClick={() => addTodo()}>
                To-do list
              </EmptyCanvasAction>
              <EmptyCanvasAction icon={SquareTerminal} onClick={() => addTerminal("wsl")}>
                Terminal
              </EmptyCanvasAction>
            </div>
            <p className="pointer-events-none text-[0.65rem] text-[var(--on-surface-variant)]">
              Right-click the canvas for everything else.
            </p>
          </div>
        )}

        {workspaceMenu && (
          <div
            ref={workspaceMenuRef}
            role="menu"
            tabIndex={-1}
            aria-label="Add to workspace"
            onContextMenu={(event) => event.preventDefault()}
            className="panel-in absolute z-[80] w-56 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 shadow-[0_14px_36px_var(--shadow-elevated)] outline-none backdrop-blur-2xl"
            style={{ left: workspaceMenu.x, top: workspaceMenu.y }}
          >
            {orderMenuGroups([
              {
                key: "documents",
                element: (
                  <WorkspaceSubmenu key="documents" label="Documents" side={workspaceMenu.submenuSide}>
                    <AddWorkspaceAction
                      disabled={photoBusy}
                      onClick={runWorkspaceMenuAction(() => void addDocument(), "documents")}
                    >
                      <MenuChip icon={FileText} />
                      Document…
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addNote(), "documents")}>
                      <MenuChip icon={StickyNote} />
                      Note
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTodo(), "documents")}>
                      <MenuChip icon={ListChecks} />
                      To-do list
                    </AddWorkspaceAction>
                    <AddWorkspaceAction
                      disabled={photoBusy}
                      onClick={runWorkspaceMenuAction(addPhoto, "documents")}
                    >
                      <MenuChip icon={ImagePlus} />
                      Photo
                    </AddWorkspaceAction>
                  </WorkspaceSubmenu>
                ),
              },
              {
                key: "ai",
                element: (
                  <WorkspaceSubmenu key="ai" label="AI" side={workspaceMenu.submenuSide}>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTerminal("claude"), "ai")}>
                      <BrandChip brand="claude" className="size-6 rounded-[0.45rem]" iconClassName="size-3.5" />
                      Claude Code
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTerminal("codex"), "ai")}>
                      <BrandChip brand="codex" className="size-6 rounded-[0.45rem]" iconClassName="size-3.5" />
                      Codex
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTerminal("opencode"), "ai")}>
                      <BrandChip brand="opencode" className="size-6 rounded-[0.45rem]" iconClassName="size-3.5" />
                      OpenCode
                    </AddWorkspaceAction>
                  </WorkspaceSubmenu>
                ),
              },
              {
                key: "terminals",
                element: (
                  <WorkspaceSubmenu key="terminals" label="Terminals" side={workspaceMenu.submenuSide}>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTerminal("cmd"), "terminals")}>
                      <MenuChip icon={SquareTerminal} />
                      CMD
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(() => addTerminal("wsl"), "terminals")}>
                      <MenuChip icon={SquareTerminal} />
                      WSL
                    </AddWorkspaceAction>
                  </WorkspaceSubmenu>
                ),
              },
              {
                key: "editors",
                element: (
                  <WorkspaceSubmenu key="editors" label="Code editors" side={workspaceMenu.submenuSide}>
                    {editorIntegrations
                      .filter((editor) => editor.enabled)
                      .map((editor) => {
                        const usable = editor.embedded || editor.available
                        return (
                          <AddWorkspaceAction
                            key={editor.id}
                            disabled={!usable}
                            title={
                              usable
                                ? `Open the project in ${editor.name} inside the workspace`
                                : `${editor.name} is not installed on this computer`
                            }
                            onClick={
                              usable
                                ? runWorkspaceMenuAction(() => addEditor(editor), "editors")
                                : undefined
                            }
                          >
                            {brandGlyphs[editor.id] ? (
                              <BrandChip brand={editor.id} className="size-6 rounded-[0.45rem]" iconClassName="size-3.5" />
                            ) : (
                              <MenuChip icon={Pencil} />
                            )}
                            <span className="min-w-0 flex-1 truncate">{editor.name}</span>
                            {!usable && (
                              <span className="shrink-0 rounded-full bg-[var(--surface-container)] px-1.5 py-px text-[0.55rem] font-medium text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)]">
                                Not installed
                              </span>
                            )}
                          </AddWorkspaceAction>
                        )
                      })}
                  </WorkspaceSubmenu>
                ),
              },
              {
                key: "tools",
                element: (
                  <WorkspaceSubmenu key="tools" label="Tools" side={workspaceMenu.submenuSide}>
                    <AddWorkspaceAction
                      onClick={runWorkspaceMenuAction(() => addBrowser(normalizeBrowserURL("")), "tools")}
                    >
                      <MenuChip icon={Globe2} />
                      Browser
                    </AddWorkspaceAction>
                    <AddWorkspaceAction onClick={runWorkspaceMenuAction(addPlayer, "tools")}>
                      <BrandChip brand="spotify" className="size-6 rounded-[0.45rem]" iconClassName="size-3.5" />
                      Spotify player
                    </AddWorkspaceAction>
                  </WorkspaceSubmenu>
                ),
              },
            ], menuGroupOrder).map((group) => group.element)}
          </div>
        )}

      </div>

      {projectMode === "app" && (
        <AppView
          project={project}
          context={activeContext}
          onSelectExperiment={selectExperiment}
          onSelectedAppId={setSelectedAppId}
          terminalSessions={appTerminalSessions}
          onOpenTerminal={openTerminalForApp}
          onFocusTerminal={focusAppTerminal}
        />
      )}
      {projectMode === "server-lab" && (
        <ServerLabView
          project={project}
          context={activeContext}
          onSelectExperiment={selectExperiment}
          initialServerId={initialServerId}
          onOpenApp={() => setProjectMode("app")}
        />
      )}

      <div className={cn("absolute bottom-4 right-4 z-40", projectMode !== "workspace" && "invisible pointer-events-none")}>
        <details
          ref={zoomDetailsRef}
          className="relative"
          onBlur={(event) => {
            if (!event.currentTarget.contains(event.relatedTarget)) closeZoomMenu()
          }}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              event.preventDefault()
              closeZoomMenu()
              zoomSummaryRef.current?.focus()
            }
          }}
        >
          <summary
            ref={zoomSummaryRef}
            aria-label={`Zoom: ${Math.round(viewport.zoom * 100)}%. Open options`}
            className="flex h-8 min-w-14 cursor-pointer list-none items-center justify-center rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-3 text-xs font-medium text-[var(--on-surface)] shadow-[0_4px_14px_var(--shadow-elevated)] outline-none backdrop-blur-xl transition-colors hover:bg-[var(--surface-container)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&::-webkit-details-marker]:hidden"
          >
            {Math.round(viewport.zoom * 100)}%
          </summary>

          <div
            aria-label="Zoom options"
            className="zoom-menu absolute bottom-full right-0 mb-2 w-52 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 shadow-[0_14px_36px_var(--shadow-elevated)] backdrop-blur-2xl"
          >
            <ZoomAction
              shortcut="Ctrl +"
              onClick={runZoomAction(() => zoomFromCenter(viewport.zoom + 0.1))}
            >
              Zoom in
            </ZoomAction>
            <ZoomAction
              shortcut="Ctrl −"
              onClick={runZoomAction(() => zoomFromCenter(viewport.zoom - 0.1))}
            >
              Zoom out
            </ZoomAction>
            <ZoomAction
              shortcut="⇧ 0"
              onClick={runZoomAction(() => zoomFromCenter(1))}
            >
              Zoom to 100%
            </ZoomAction>
            <ZoomAction
              shortcut="⇧ 1"
              onClick={runZoomAction(() => zoomToNodes(nodesRef.current))}
            >
              Fit content
            </ZoomAction>
            <ZoomAction
              shortcut="⇧ 2"
              disabled={!selectedID}
              title={selectedID ? undefined : "Select a panel first"}
              onClick={runZoomAction(() => {
                const selected = nodesRef.current.find(
                  (node) => node.id === selectedID,
                )
                if (selected) zoomToNodes([selected])
              })}
            >
              Fit selection
            </ZoomAction>
            <ZoomAction
              shortcut="⇧ 3"
              disabled={nodes.length === 0}
              onClick={runZoomAction(tidyAll)}
            >
              Tidy up
            </ZoomAction>
          </div>
        </details>
      </div>

      <form
        aria-label="Workspace command bar"
        onSubmit={runCommand}
        className={cn(
          "absolute bottom-5 left-1/2 z-40 w-[calc(100%-2rem)] -translate-x-1/2 rounded-[1.4rem] border border-[var(--focus-border)] bg-[var(--surface-container-high)] px-3 py-1 shadow-[0_14px_40px_var(--shadow-elevated)] backdrop-blur-2xl transition-[box-shadow,max-width] duration-300 focus-within:shadow-[0_14px_40px_var(--shadow-elevated),0_0_0_3px_var(--focus-ring)]",
          wsChatOpen && wsChatSize === "large" ? "max-w-[42rem]" : "max-w-[33rem]",
          projectMode !== "workspace" && "invisible pointer-events-none",
        )}
      >
        {wsChatOpen && (
          <div
            className={cn(
              "chat-morph -mx-3 -mt-1 mb-1 flex flex-col overflow-hidden rounded-t-[1.4rem] border-b border-[var(--outline-variant)] transition-[height] duration-300 ease-[cubic-bezier(.22,1,.36,1)]",
              wsChatSize === "large" ? "h-[30rem]" : "h-[19rem]",
            )}
          >
            <AssistantChat
              messages={wsChatMessages}
              busy={aiWorking}
              chatId={workspaceChatId}
              size={wsChatSize}
              onToggleSize={() =>
                setWsChatSize((current) => (current === "compact" ? "large" : "compact"))
              }
              onLoadChat={(id, messages) => {
                setWorkspaceChatId(id)
                setWsChatMessages(messages)
              }}
              onNewChat={() => {
                setWorkspaceChatId("")
                setWsChatMessages([])
              }}
              onClose={() => setWsChatOpen(false)}
            />
          </div>
        )}
        <div className="flex h-9 items-center gap-2 text-[var(--on-surface-variant)]">
          <details
            ref={backgroundDetailsRef}
            className="relative shrink-0"
            onBlur={(event) => {
              if (!event.currentTarget.contains(event.relatedTarget)) {
                closeBackgroundMenu()
              }
            }}
            onKeyDown={(event) => {
              if (event.key === "Escape") {
                event.preventDefault()
                closeBackgroundMenu()
                backgroundSummaryRef.current?.focus()
              }
            }}
          >
            <summary
              ref={backgroundSummaryRef}
              aria-label="Workspace options"
              title="Workspace options"
              className="flex size-7 cursor-pointer list-none items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&::-webkit-details-marker]:hidden"
            >
              <Plus className="size-4" strokeWidth={2} />
            </summary>
            <div
              role="menu"
              aria-label="Workspace background"
              className="add-menu absolute bottom-full left-0 z-50 mb-2 w-56 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 shadow-[0_14px_36px_var(--shadow-elevated)] backdrop-blur-2xl"
            >
              <ProjectMenuAction
                disabled={backgroundBusy}
                onClick={runBackgroundAction(chooseWorkspaceBackground)}
              >
                <ImagePlus className="size-4" />
                <span>Change background</span>
              </ProjectMenuAction>
              <ProjectMenuAction
                disabled={backgroundBusy || !workspaceBackground}
                onClick={runBackgroundAction(clearWorkspaceBackground)}
              >
                <RotateCcw className="size-4" />
                <span>Default background</span>
              </ProjectMenuAction>
            </div>
          </details>
          <Input
            autoFocus
            value={command}
            onChange={(event) => setCommand(event.target.value)}
            placeholder="What would you like to change or create?"
            aria-label="Command"
            className="h-8 min-w-0 flex-1 border-0 bg-transparent px-0 py-0 text-sm shadow-none placeholder:text-[var(--on-surface-variant)] focus-visible:ring-0"
          />
          <Button
            type="submit"
            variant="ghost"
            size="icon"
            aria-label="Run command"
            disabled={!command.trim()}
            className="size-7 rounded-full text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]"
          >
            <ArrowUp className="size-4 translate-y-px" strokeWidth={2} />
          </Button>
        </div>
        {(commandMessage || showHelp) && (
          <div
            role={commandMessage.startsWith("Unknown command") ? "alert" : "status"}
            className="mx-2 mb-1 mt-1 rounded-xl bg-[var(--surface-container)] px-3 py-2 text-[0.68rem] text-[var(--on-surface-variant)]"
          >
            {commandMessage}
            {showHelp && (
              <p className="mt-1">
                <code>nota</code> · <code>tareas</code> · <code>abrir</code> ·{" "}
                <code>ordenar</code> ·{" "}
                <code>cmd</code> · <code>wsl</code> · <code>codex</code> ·{" "}
                <code>claude</code> ·{" "}
                <code>browser/navegador [url]</code> ·{" "}
                <code>folder/carpeta</code> · <code>help/ayuda</code>
              </p>
            )}
          </div>
        )}
      </form>

      {dock && dock.items.length > 0 && (
        <aside
          ref={dockRef}
          aria-label="Open projects"
          className="view-enter absolute left-3 top-1/2 z-[85] flex -translate-y-1/2 flex-col items-center gap-1.5 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container)] p-1.5 shadow-[0_1px_3px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated)] backdrop-blur-xl"
        >
          {dock.items.map((item) => (
            <div key={item.id} className="group relative">
              <Tooltip>
                <TooltipTrigger asChild>
                  <button
                    type="button"
                    aria-label={`Go to ${item.name}`}
                    aria-pressed={item.active}
                    onClick={() => {
                      setDockPickerOpen(false)
                      dock.onSelect(item.id)
                    }}
                    className={cn(
                      "flex size-10 items-center justify-center overflow-hidden rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] text-[0.68rem] font-semibold text-[var(--on-surface-variant)] outline-none transition-[box-shadow,transform,color,opacity] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.96]",
                      item.active && "ring-2 ring-[var(--primary)]",
                      !item.live && "opacity-55 saturate-50 hover:opacity-90",
                    )}
                  >
                    {item.thumbnail ? (
                      <img
                        src={item.thumbnail}
                        alt=""
                        draggable={false}
                        className="size-full object-cover"
                      />
                    ) : (
                      item.name.slice(0, 2).toUpperCase()
                    )}
                  </button>
                </TooltipTrigger>
                <TooltipContent side="right">
                  {item.name}
                  {!item.live && " · paused"}
                </TooltipContent>
              </Tooltip>
              <button
                type="button"
                aria-label={`Close ${item.name}`}
                onClick={() => dock.onClose(item.id)}
                className="absolute -right-0.5 -top-0.5 z-10 flex size-4 items-center justify-center rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] text-[var(--on-surface-variant)] opacity-0 shadow-[0_1px_3px_var(--shadow-soft)] outline-none transition-opacity hover:bg-[var(--error)] hover:text-white focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-[var(--ring)] group-hover:opacity-100"
              >
                <X className="size-2.5" strokeWidth={2.2} />
              </button>
            </div>
          ))}
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label="Open another project"
                aria-expanded={dockPickerOpen}
                onClick={() => {
                  setDockQuery("")
                  setDockPickerOpen((current) => !current)
                }}
                className={cn(
                  "flex size-10 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                  dockPickerOpen && "bg-[var(--primary-container)] text-[var(--on-primary-container)]",
                )}
              >
                <Plus
                  className={cn("size-4 transition-transform", dockPickerOpen && "rotate-45")}
                  strokeWidth={1.8}
                />
              </button>
            </TooltipTrigger>
            <TooltipContent side="right">Open another project</TooltipContent>
          </Tooltip>

          {dockPickerOpen && (
            <div className="panel-in absolute bottom-0 left-full ml-2 w-64 overflow-hidden rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_1px_3px_var(--shadow-soft),0_16px_40px_var(--shadow-elevated)] backdrop-blur-2xl">
              <div className="border-b border-[var(--outline-variant)] p-2">
                <Input
                  autoFocus
                  value={dockQuery}
                  onChange={(event) => setDockQuery(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Escape") {
                      event.preventDefault()
                      setDockPickerOpen(false)
                    }
                  }}
                  placeholder="Search project…"
                  className="h-8 rounded-lg border-[var(--outline-variant)] bg-[var(--surface-container)] text-xs"
                />
              </div>
              <div className="max-h-64 overflow-y-auto p-1.5">
                {dock.candidates
                  .filter((candidate) =>
                    candidate.name.toLowerCase().includes(dockQuery.trim().toLowerCase()),
                  )
                  .map((candidate) => (
                    <button
                      key={candidate.id}
                      type="button"
                      onClick={() => {
                        setDockPickerOpen(false)
                        dock.onOpenProject(candidate.id)
                      }}
                      className="flex w-full items-center gap-2.5 rounded-xl px-2 py-1.5 text-left outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:bg-[var(--state-layer)]"
                    >
                      <span className="flex size-7 shrink-0 items-center justify-center overflow-hidden rounded-lg bg-[var(--primary-container)] text-[0.6rem] font-semibold text-[var(--on-primary-container)]">
                        {candidate.thumbnail ? (
                          <img
                            src={candidate.thumbnail}
                            alt=""
                            draggable={false}
                            className="size-full object-cover"
                          />
                        ) : (
                          candidate.name.slice(0, 2).toUpperCase()
                        )}
                      </span>
                      <span className="min-w-0 flex-1 truncate text-xs font-medium">
                        {candidate.name}
                      </span>
                      {candidate.open && (
                        <span
                          aria-label="Already open"
                          className="size-1.5 shrink-0 rounded-full bg-[var(--success)]"
                        />
                      )}
                    </button>
                  ))}
                {dock.candidates.filter((candidate) =>
                  candidate.name.toLowerCase().includes(dockQuery.trim().toLowerCase()),
                ).length === 0 && (
                  <p className="px-2 py-3 text-center text-xs text-[var(--on-surface-variant)]">
                    No results
                  </p>
                )}
              </div>
            </div>
          )}
        </aside>
      )}

      {assistantActivity && projectMode === "workspace" && (
        <div
          role="status"
          aria-live="polite"
          className="view-enter absolute bottom-[4.6rem] left-1/2 z-40 flex max-w-[min(26rem,calc(100%-2rem))] -translate-x-1/2 items-center gap-2 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 py-1.5 text-[0.68rem] font-medium text-[var(--on-surface-variant)] shadow-[0_1px_2px_var(--shadow-soft),0_8px_24px_var(--shadow-elevated)] backdrop-blur-xl"
        >
          <span
            aria-hidden="true"
            className={cn(
              "size-1.5 shrink-0 rounded-full",
              assistantActivity.tone === "error"
                ? "bg-[var(--error)]"
                : "bg-[var(--success)]",
            )}
          />
          <span className="truncate">{assistantActivity.message}</span>
        </div>
      )}

      {showFirstRunHint && loaded && projectMode === "workspace" && (
        <div className="view-enter absolute bottom-24 right-4 z-40 w-64 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 shadow-[0_1px_3px_var(--shadow-soft),0_16px_40px_var(--shadow-elevated)] backdrop-blur-2xl">
          <p className="text-xs font-semibold tracking-[-0.01em]">
            Welcome to your board
          </p>
          <ul className="mt-2 space-y-1.5 text-[0.68rem] leading-5 text-[var(--on-surface-variant)]">
            <li>Drop any file here to open it.</li>
            <li>Right-click for notes, to-dos, and more.</li>
            <li>Drag panels around; Ctrl+Z undoes.</li>
            <li>Ctrl+K searches and does everything.</li>
          </ul>
          <Button
            type="button"
            variant="outline"
            className="mt-3 h-7 w-full rounded-full text-[0.68rem]"
            onClick={dismissFirstRunHint}
          >
            Got it
          </Button>
        </div>
      )}

      {notice && projectMode === "workspace" && (
        <div
          key={`${notice.tone}-${notice.message}`}
          role={notice.tone === "error" ? "alert" : "status"}
          aria-live={notice.tone === "error" ? "assertive" : "polite"}
          className="notice-auto-out absolute bottom-4 left-4 z-50 flex max-w-[min(28rem,calc(100%-8rem))] items-center gap-2 rounded-full border border-[var(--outline-variant)] bg-[var(--tooltip)] px-4 py-2 text-xs font-medium text-[var(--tooltip-foreground)] shadow-[0_8px_24px_var(--shadow-elevated)]"
          title={notice.message}
        >
          {notice.tone === "error" ? (
            <CircleAlert className="size-3.5 shrink-0 text-[var(--error)]" />
          ) : (
            <Check className="size-3.5 shrink-0 text-[var(--primary)]" />
          )}
          <span className="truncate">{notice.message}</span>
        </div>
      )}
    </section>
  )
}

function ProjectMenuAction({
  shortcut,
  destructive = false,
  className,
  children,
  ...props
}: ComponentProps<"button"> & {
  shortcut?: string
  destructive?: boolean
}) {
  return (
    <button
      type="button"
      className={cn(
        "flex h-9 w-full items-center gap-3 rounded-xl px-3 text-left text-xs text-[var(--on-surface)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:bg-[var(--state-layer)] disabled:cursor-default disabled:opacity-35",
        destructive && "text-[var(--error)]",
        className,
      )}
      {...props}
    >
      {children}
      {shortcut && (
        <kbd className="ml-auto shrink-0 font-sans text-[0.65rem] font-normal text-[var(--on-surface-variant)]">
          {shortcut}
        </kbd>
      )}
    </button>
  )
}

function ZoomAction({
  shortcut,
  className,
  children,
  ...props
}: ComponentProps<"button"> & { shortcut: string }) {
  return (
    <button
      type="button"
      className={cn(
        "flex h-9 w-full items-center justify-between gap-4 rounded-xl px-2.5 text-left text-xs text-[var(--on-surface)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:bg-[var(--state-layer)] disabled:cursor-default disabled:opacity-35",
        className,
      )}
      {...props}
    >
      <span>{children}</span>
      <kbd className="shrink-0 font-sans text-[0.65rem] font-normal text-[var(--on-surface-variant)]">
        {shortcut}
      </kbd>
    </button>
  )
}

function MenuChip({ icon: Icon }: { icon: LucideIcon }) {
  return (
    <span
      aria-hidden="true"
      className="flex size-6 shrink-0 items-center justify-center rounded-[0.45rem] bg-[var(--primary-container)] text-[var(--on-primary-container)]"
    >
      <Icon className="size-3.5" strokeWidth={1.7} />
    </span>
  )
}

function AddWorkspaceAction({
  className,
  children,
  ...props
}: ComponentProps<"button">) {
  return (
    <button
      type="button"
      role="menuitem"
      className={cn(
        "flex h-9 w-full items-center gap-3 rounded-xl px-2.5 text-left text-xs text-[var(--on-surface)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:bg-[var(--state-layer)] disabled:cursor-default disabled:opacity-35",
        className,
      )}
      {...props}
    >
      {children}
    </button>
  )
}

function WorkspaceSubmenu({
  label,
  side,
  children,
}: {
  label: string
  side: "left" | "right"
  children: React.ReactNode
}) {
  return (
    <div className="group relative">
      <button
        type="button"
        role="menuitem"
        aria-haspopup="menu"
        className="flex h-9 w-full items-center gap-3 rounded-lg px-2.5 text-left text-xs text-[var(--on-surface)] outline-none transition-colors hover:bg-[var(--state-layer)] focus:bg-[var(--state-layer)]"
      >
        <span>{label}</span>
        <ChevronRight className={cn("ml-auto size-3.5", side === "left" && "rotate-180")} />
      </button>
      <div
        role="menu"
        aria-label={label}
        className={cn(
          "invisible absolute top-0 z-10 w-52 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 opacity-0 shadow-[0_14px_36px_var(--shadow-elevated)] backdrop-blur-2xl transition-[opacity,visibility] group-hover:visible group-hover:opacity-100 group-focus-within:visible group-focus-within:opacity-100",
          side === "left" ? "right-full" : "left-full",
        )}
      >
        {children}
      </div>
    </div>
  )
}

function WorkspacePanel({
  node,
  panelRef,
  selected,
  maximized,
  obscured,
  activity,
  onSelect,
  onMove,
  onMoveKeyboard,
  onResize,
  onResizeKeyboard,
  onToggleMaximize,
  onClose,
  children,
}: {
  node: WorkspaceNode
  panelRef?: (element: HTMLElement | null) => void
  selected: boolean
  maximized: boolean
  obscured: boolean
  activity?: "working" | "waiting"
  onSelect: () => void
  onMove: (event: ReactPointerEvent<HTMLElement>) => void
  onMoveKeyboard: (x: number, y: number) => void
  onResize: (event: ReactPointerEvent<HTMLButtonElement>) => void
  onResizeKeyboard: (width: number, height: number) => void
  onToggleMaximize: () => void
  onClose: () => void
  children: React.ReactNode
}) {
  const [animatingGeometry, setAnimatingGeometry] = useState(false)
  const skipGeometryAnimationRef = useRef(true)

  useEffect(() => {
    if (skipGeometryAnimationRef.current) {
      skipGeometryAnimationRef.current = false
      return
    }
    setAnimatingGeometry(true)
    const timeout = window.setTimeout(() => setAnimatingGeometry(false), 340)
    return () => window.clearTimeout(timeout)
  }, [maximized])


  const title = node.type === "terminal"
    ? (node.taskHint ?? terminalTitles[node.shell])
    : node.type === "browser"
      ? "Browser"
      : node.type === "player"
        ? "Spotify"
        : node.type === "photo"
          ? "Photo"
          : node.type === "note"
            ? "Note"
            : node.type === "todo"
              ? "To-do"
              : node.type === "document"
                ? node.name
                : editorTitles[node.editorId] ?? node.editorId

  return (
    <article
      ref={panelRef}
      aria-label={`${title} panel`}
      data-panel-type={node.type}
      onPointerDown={onSelect}
      className={cn(
        "panel-in wobble-pop pointer-events-auto absolute left-0 top-0 flex flex-col overflow-hidden rounded-xl border bg-[var(--surface-container-high)] shadow-[0_12px_32px_var(--shadow-elevated)]",
        animatingGeometry &&
          "transition-[translate,width,height,border-radius] duration-300 ease-[cubic-bezier(.22,1,.36,1)]",
        obscured && "invisible pointer-events-none",
        maximized && "rounded-none",
        selected
          ? "border-[var(--focus-border)] ring-2 ring-[var(--focus-ring)]"
          : activity === "working"
            ? "border-[var(--success)] ring-1 ring-[var(--success)]"
            : activity === "waiting"
              ? "border-[var(--warning)] ring-1 ring-[var(--warning)]"
              : "border-[var(--outline-variant)]",
      )}
      style={{
        // translate (no left/top): moving the panel composites on the GPU without relayout
        translate: maximized ? "0px 0px" : `${node.x}px ${node.y}px`,
        width: maximized ? "100%" : node.width,
        height: maximized ? "100%" : node.height,
        zIndex: maximized ? 60 : node.z,
      }}
    >
      <div
        tabIndex={0}
        aria-label={`Move panel ${title}; use the arrow keys to move it`}
        onFocus={onSelect}
        onPointerDown={maximized ? undefined : onMove}
        onKeyDown={(event) => {
          if (maximized) return
          const step = event.shiftKey ? 1 : 10
          const movement =
            event.key === "ArrowLeft"
              ? [-step, 0]
              : event.key === "ArrowRight"
                ? [step, 0]
                : event.key === "ArrowUp"
                  ? [0, -step]
                  : event.key === "ArrowDown"
                    ? [0, step]
                    : null
          if (!movement) return
          event.preventDefault()
          onMoveKeyboard(movement[0], movement[1])
        }}
        className={cn(
          "flex h-10 shrink-0 cursor-move items-center gap-2.5 border-b border-[var(--outline-variant)] bg-[var(--surface-container)] px-2.5 outline-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-[var(--ring)]",
          maximized && "h-14 pl-20 pr-32",
        )}
      >
        <PanelIcon node={node} />
        <span className="min-w-0 flex-1 truncate text-[0.7rem] font-semibold tracking-[-0.01em]">
          {title}
        </span>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={`${maximized ? "Restore" : "Maximize"} panel ${title}`}
              onPointerDown={(event) => event.stopPropagation()}
              onClick={onToggleMaximize}
              className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
            >
              {maximized ? (
                <Minimize2 className="size-3.5" />
              ) : (
                <Maximize2 className="size-3.5" />
              )}
            </button>
          </TooltipTrigger>
          <TooltipContent side="bottom">
            {maximized ? "Restore" : "Maximize"}
          </TooltipContent>
        </Tooltip>
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label={`Close panel ${title}`}
              onPointerDown={(event) => event.stopPropagation()}
              onClick={onClose}
              className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--error)] hover:text-white focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
            >
              <X className="size-3.5" />
            </button>
          </TooltipTrigger>
          <TooltipContent side="bottom">Close</TooltipContent>
        </Tooltip>
      </div>

      <div className="min-h-0 flex-1">{children}</div>

      {!maximized && (
        <button
          type="button"
          aria-label={`Resize panel ${title}; use the arrow keys`}
          title="Resize"
          onPointerDown={onResize}
          onKeyDown={(event) => {
            const step = event.shiftKey ? 1 : 10
            const size =
              event.key === "ArrowLeft"
                ? [-step, 0]
                : event.key === "ArrowRight"
                  ? [step, 0]
                  : event.key === "ArrowUp"
                    ? [0, -step]
                    : event.key === "ArrowDown"
                      ? [0, step]
                      : null
            if (!size) return
            event.preventDefault()
            onResizeKeyboard(size[0], size[1])
          }}
          className="absolute bottom-0 right-0 z-10 size-7 cursor-se-resize rounded-tl-xl border-l border-t border-[var(--outline-variant)] bg-[var(--surface-container)] outline-none transition-colors after:absolute after:bottom-1.5 after:right-1.5 after:size-3 after:border-b-2 after:border-r-2 after:border-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
        />
      )}
    </article>
  )
}

function WorkspacePhotoPanel({
  node,
  onRetry,
  onImageError,
}: {
  node: PhotoNode
  onRetry: () => void
  onImageError: () => void
}) {
  if (node.status === "loading") {
    return (
      <div className="flex h-full items-center justify-center text-xs text-[var(--on-surface-variant)]">
        <LoaderCircle className="mr-2 size-4 animate-spin" />
        Loading photo
      </div>
    )
  }

  if (node.status === "error" || !node.dataURL) {
    return (
      <div className="flex h-full flex-col items-center justify-center gap-3 px-6 text-center">
        <CircleAlert className="size-5 text-[var(--error)]" />
        <p className="max-w-sm text-xs text-[var(--on-surface-variant)]">
          {node.error ?? "Could not load photo"}
        </p>
        <Button
          type="button"
          variant="outline"
          className="h-8 px-3 text-xs"
          onClick={onRetry}
        >
          Retry
        </Button>
      </div>
    )
  }

  return (
    <div className="h-full bg-[var(--surface)] p-1">
      <img
        src={node.dataURL}
        alt="Photo added to the workspace"
        draggable={false}
        onError={onImageError}
        className="h-full w-full select-none object-contain"
      />
    </div>
  )
}

function extractArtworkTint(src: string): Promise<string> {
  return new Promise((resolve, reject) => {
    const image = new Image()
    image.onload = () => {
      const size = 24
      const canvas = document.createElement("canvas")
      canvas.width = size
      canvas.height = size
      const context = canvas.getContext("2d")
      if (!context) {
        reject(new Error("canvas not available"))
        return
      }
      context.drawImage(image, 0, 0, size, size)
      const { data } = context.getImageData(0, 0, size, size)
      const pixels: Array<[number, number, number, number]> = []
      for (let index = 0; index < data.length; index += 4) {
        const red = data[index]
        const green = data[index + 1]
        const blue = data[index + 2]
        const max = Math.max(red, green, blue)
        const min = Math.min(red, green, blue)
        const luminance = (max + min) / 2
        if (luminance < 24 || luminance > 236) continue
        pixels.push([max - min, red, green, blue])
      }
      if (pixels.length === 0) {
        reject(new Error("artwork has no usable color"))
        return
      }
      // ponytail: average of the most saturated quartile; k-means would give the exact color if ever needed
      pixels.sort((a, b) => b[0] - a[0])
      const vivid = pixels.slice(0, Math.max(1, Math.floor(pixels.length / 4)))
      const total = vivid.reduce(
        (sum, [, red, green, blue]) => [sum[0] + red, sum[1] + green, sum[2] + blue],
        [0, 0, 0],
      )
      resolve(
        total
          .map((channel) => Math.round(channel / vivid.length))
          .join(" "),
      )
    }
    image.onerror = () => reject(new Error("could not read the artwork"))
    image.src = src
  })
}

function SpotifyPlayerPanel() {
  const [playback, setPlayback] = useState<SpotifyPlayback | null>(null)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState<SpotifyAction | "">("")
  const [error, setError] = useState("")
  const [tint, setTint] = useState<string | null>(null)
  const [artworkDataURL, setArtworkDataURL] = useState("")
  const mountedRef = useRef(false)
  const busyRef = useRef<SpotifyAction | "">("")
  const requestVersionRef = useRef(0)
  const trackKeyRef = useRef("")
  const rootRef = useRef<HTMLDivElement>(null)

  useEffect(() => {
    if (!artworkDataURL) {
      setTint(null)
      return
    }
    let cancelled = false
    extractArtworkTint(artworkDataURL)
      .then((next) => {
        if (!cancelled) setTint(next)
      })
      .catch(() => {
        if (!cancelled) setTint(null)
      })
    return () => {
      cancelled = true
    }
  }, [artworkDataURL])

  const requestPlayback = async (action?: SpotifyAction) => {
    if (action && busyRef.current) return
    if (action) {
      busyRef.current = action
      setBusy(action)
    }
    const version = ++requestVersionRef.current

    try {
      const next = action && action !== "refresh"
        ? await ControlSpotifyPlayback(action)
        : await GetSpotifyPlaybackSince(trackKeyRef.current)
      if (!mountedRef.current || version !== requestVersionRef.current) return
      setPlayback(next)
      // The artwork is only sent when the track changes; if it didn't come, keep the previous one.
      if (next.artworkDataURL) {
        setArtworkDataURL(next.artworkDataURL)
        trackKeyRef.current = next.trackKey ?? ""
      } else if ((next.trackKey ?? "") !== trackKeyRef.current) {
        setArtworkDataURL("")
        trackKeyRef.current = next.trackKey ?? ""
      }
      setError(next.errorMessage?.trim() ?? "")
    } catch (caught) {
      if (mountedRef.current && version === requestVersionRef.current) {
        setError(errorMessage(caught))
      }
    } finally {
      if (!mountedRef.current) return
      if (action && busyRef.current === action) {
        busyRef.current = ""
        setBusy("")
      }
      if (version === requestVersionRef.current) setLoading(false)
    }
  }

  useEffect(() => {
    mountedRef.current = true
    let active = true
    let timer: number | undefined
    const poll = async () => {
      // No polling while the window is minimized or the panel is hidden
      // (workspace paused or in another mode): offsetParent is null with display:none.
      const visible =
        !document.hidden && rootRef.current?.offsetParent !== null
      if (!busyRef.current && visible) await requestPlayback()
      if (active) {
        timer = window.setTimeout(poll, spotifyPollDelay)
      }
    }
    void poll()

    return () => {
      active = false
      mountedRef.current = false
      requestVersionRef.current += 1
      window.clearTimeout(timer)
    }
  }, [])

  const available = playback?.available === true
  const playing = playback?.playbackStatus === "playing"
  const disabled = loading || busy !== "" || !available
  const controlClass =
    "flex size-7 items-center justify-center rounded-full text-[var(--on-surface)] outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:cursor-default disabled:opacity-35"

  if (loading && !playback) {
    return (
      <div ref={rootRef} role="status" className="flex size-full items-center justify-center gap-2 text-xs text-[var(--on-surface-variant)]">
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        {"Looking for a Spotify session\u2026"}
      </div>
    )
  }

  if (!available) {
    return (
      <div
        ref={rootRef}
        role={error ? "alert" : "status"}
        className="flex size-full flex-col items-center justify-center gap-3 p-6 text-center"
      >
        <span className="flex size-14 items-center justify-center rounded-full bg-[var(--primary-container)] text-[var(--on-primary-container)]">
          <Music2 className="size-6" strokeWidth={1.6} />
        </span>
        <div>
          <p className="text-sm font-semibold">
            {error ? "Could not query Spotify" : "Spotify isn't playing"}
          </p>
          <p className={cn(
            "mt-1 max-w-sm text-xs leading-5",
            error ? "text-[var(--error)]" : "text-[var(--on-surface-variant)]",
          )}>
            {error || "Open Spotify and play a song to control it from Seizen."}
          </p>
        </div>
        <button
          type="button"
          onClick={() => void requestPlayback("refresh")}
          disabled={busy !== ""}
          className="flex h-9 items-center gap-2 rounded-full bg-[var(--primary)] px-4 text-xs font-semibold text-[var(--on-primary)] outline-none hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-50"
        >
          {busy === "refresh" ? (
            <LoaderCircle className="size-4 animate-spin" />
          ) : (
            <RefreshCw className="size-4" />
          )}
          Refresh
        </button>
      </div>
    )
  }

  const duration = Math.max(0, playback.durationSeconds || 0)
  const position = Math.min(duration, Math.max(0, playback.positionSeconds || 0))

  return (
    <div
      ref={rootRef}
      aria-live="polite"
      style={tint ? ({ "--spotify-tint": tint } as CSSProperties) : undefined}
      className="relative flex size-full min-h-0 items-center gap-3 overflow-hidden p-3"
    >
      {tint && (
        <div
          aria-hidden="true"
          className={cn("spotify-ambient absolute inset-0", !playing && "spotify-ambient--paused")}
        >
          <span className="spotify-wave spotify-wave--back" />
          <span className="spotify-wave spotify-wave--front" />
        </div>
      )}

      {artworkDataURL ? (
        <img
          src={artworkDataURL}
          alt=""
          draggable={false}
          className="relative z-10 size-24 shrink-0 rounded-xl object-cover"
          style={{
            boxShadow: tint
              ? `0 6px 24px rgb(${tint} / 0.45)`
              : "0 6px 18px var(--shadow-soft)",
          }}
        />
      ) : (
        <span className="relative z-10 flex size-24 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
          <Music2 className="size-8" strokeWidth={1.6} />
        </span>
      )}

      <div className="relative z-10 flex min-w-0 flex-1 flex-col justify-center self-stretch">
        <p className="truncate text-sm font-semibold tracking-[-0.02em]">
          {playback.title || "Untitled"}
        </p>
        <p className="mt-0.5 truncate text-[0.68rem] text-[var(--on-surface-variant)]">
          {playback.artist || "Unknown artist"}
          {playback.album ? ` \u00b7 ${playback.album}` : ""}
        </p>

        {duration > 0 && (
          <div className="mt-2">
            <div
              role="progressbar"
              aria-label="Song progress"
              aria-valuemin={0}
              aria-valuemax={duration}
              aria-valuenow={position}
              className="h-1 overflow-hidden rounded-full bg-[var(--outline-variant)]"
            >
              <span
                className="block h-full rounded-full bg-[var(--primary)] transition-[width] duration-500"
                style={{
                  width: `${(position / duration) * 100}%`,
                  background: tint ? `rgb(${tint})` : undefined,
                }}
              />
            </div>
            <div className="mt-1 flex justify-between text-[0.58rem] tabular-nums text-[var(--on-surface-variant)]">
              <span>{spotifyPlaybackTime(position)}</span>
              <span>{spotifyPlaybackTime(duration)}</span>
            </div>
          </div>
        )}

        <div className="mt-auto flex items-center justify-between pt-1">
          <span className="truncate pr-2 text-[0.58rem] text-[var(--on-surface-variant)]">
            {error || spotifyPlaybackStatus(playback.playbackStatus)}
          </span>
          <div className="flex shrink-0 items-center gap-1">
            {error && (
              <button
                type="button"
                aria-label="Retry Spotify query"
                title="Retry"
                onClick={() => void requestPlayback("refresh")}
                disabled={busy !== ""}
                className={controlClass}
              >
                {busy === "refresh" ? <LoaderCircle className="size-3.5 animate-spin" /> : <RefreshCw className="size-3.5" />}
              </button>
            )}
            <button
              type="button"
              aria-label={"Previous song"}
              title="Previous"
              onClick={() => void requestPlayback("previous")}
              disabled={disabled || !playback.canPrevious}
              className={controlClass}
            >
              {busy === "previous" ? <LoaderCircle className="size-3.5 animate-spin" /> : <SkipBack className="size-3.5" />}
            </button>
            <button
              type="button"
              aria-label={playing ? "Pause Spotify" : "Play Spotify"}
              title={playing ? "Pause" : "Play"}
              onClick={() => void requestPlayback("toggle")}
              disabled={disabled || !playback.canToggle}
              className={cn(controlClass, "size-9 bg-[var(--primary)] text-[var(--on-primary)] hover:opacity-90")}
            >
              {busy === "toggle" ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : playing ? (
                <Pause className="size-4 fill-current" />
              ) : (
                <Play className="ml-0.5 size-4 fill-current" />
              )}
            </button>
            <button
              type="button"
              aria-label={"Next song"}
              title="Next"
              onClick={() => void requestPlayback("next")}
              disabled={disabled || !playback.canNext}
              className={controlClass}
            >
              {busy === "next" ? <LoaderCircle className="size-3.5 animate-spin" /> : <SkipForward className="size-3.5" />}
            </button>
          </div>
        </div>
      </div>
    </div>
  )
}

function spotifyPlaybackTime(seconds: number) {
  const whole = Math.max(0, Math.floor(seconds))
  return `${Math.floor(whole / 60)}:${String(whole % 60).padStart(2, "0")}`
}

function spotifyPlaybackStatus(status: string) {
  return status === "playing"
    ? "Playing"
    : status === "paused"
      ? "Paused"
      : status === "changing"
        ? "Changing\u2026"
        : status === "stopped"
          ? "Stopped"
          : "Spotify connected"
}

function BrowserPanel({
  node,
  onNavigate,
}: {
  node: BrowserNode
  onNavigate: (url: string) => void
}) {
  const [draft, setDraft] = useState(node.url)
  const [error, setError] = useState("")

  useEffect(() => setDraft(node.url), [node.url])

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    try {
      const url = normalizeBrowserURL(draft)
      onNavigate(url)
      setDraft(url)
      setError("")
    } catch (caught) {
      setError(errorMessage(caught))
    }
  }

  return (
    <div className="flex size-full min-h-0 flex-col bg-[var(--surface)]">
      <form
        onSubmit={submit}
        className="flex h-10 shrink-0 items-center gap-1.5 border-b border-[var(--outline-variant)] bg-[var(--surface-container)] px-2"
      >
        <input
          value={draft}
          onChange={(event) => setDraft(event.target.value)}
          aria-label="Browser address"
          placeholder="Search Google or type an address"
          spellCheck={false}
          className="h-7 min-w-0 flex-1 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-3 text-[0.65rem] text-[var(--on-surface)] outline-none transition-[border-color,box-shadow] placeholder:text-[var(--on-surface-variant)] focus-visible:border-[var(--focus-border)] focus-visible:ring-2 focus-visible:ring-[var(--focus-ring)]"
        />
        <button
          type="submit"
          aria-label="Navigate"
          className="flex size-7 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
        >
          <ArrowRight className="size-3.5" />
        </button>
      </form>
      {error && (
        <p role="alert" className="view-enter bg-[var(--error-container)] px-3 py-1 text-[0.6rem] text-[var(--on-error-container)]">
          {error}
        </p>
      )}
      <iframe
        key={node.url}
        src={node.url}
        title={`Browser: ${node.url}`}
        sandbox="allow-forms allow-same-origin allow-scripts"
        allow="camera 'none'; microphone 'none'; geolocation 'none'; clipboard-read 'none'; clipboard-write 'none'"
        referrerPolicy="no-referrer"
        className="min-h-0 flex-1 border-0 bg-white"
      />
    </div>
  )
}

function EditorPanel({
  node,
  onRetry,
}: {
  node: EditorNode
  onRetry: () => void
}) {
  if (node.status === "starting") {
    return (
      <div
        role="status"
        className="flex size-full items-center justify-center gap-2 bg-[var(--surface)] text-xs text-[var(--on-surface-variant)]"
      >
        <LoaderCircle className="size-4 animate-spin" aria-hidden="true" />
        Starting {editorTitles[node.editorId] ?? node.editorId}…
      </div>
    )
  }
  if (node.status === "running" && node.native && node.sessionId) {
    return (
      <NativeEditorCard
        name={editorTitles[node.editorId] ?? node.editorId}
        sessionId={node.sessionId}
      />
    )
  }
  if (node.status === "error" || !node.url) {
    return (
      <div
        role="alert"
        className="flex size-full flex-col items-center justify-center gap-3 bg-[var(--surface)] p-6 text-center text-xs"
      >
        <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--error-container)] text-[var(--on-error-container)]">
          <CircleAlert className="size-[1.15rem]" strokeWidth={1.7} />
        </span>
        <span className="max-w-sm text-[var(--on-surface-variant)]">
          {node.error ?? "Editor session unavailable"}
        </span>
        <button
          type="button"
          onClick={onRetry}
          className="flex h-8 items-center gap-1.5 rounded-full bg-[var(--primary)] px-4 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-[opacity,transform] hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.97]"
        >
          <RotateCcw className="size-3.5" />
          Retry
        </button>
      </div>
    )
  }
  return (
    <iframe
      key={node.sessionId}
      src={node.url}
      title={`${editorTitles[node.editorId] ?? node.editorId} for the project`}
      sandbox="allow-downloads allow-forms allow-modals allow-popups allow-same-origin allow-scripts"
      allow="clipboard-read; clipboard-write; camera 'none'; microphone 'none'; geolocation 'none'"
      referrerPolicy="no-referrer"
      className="size-full border-0 bg-[#181818]"
    />
  )
}

// A native editor (Zed, Cursor...) runs as its own OS window — embedding it
// as a child breaks fullscreen and minimize, so the node is a small
// controller card instead.
function NativeEditorCard({ name, sessionId }: { name: string; sessionId: string }) {
  return (
    <div className="flex size-full flex-col items-center justify-center gap-3 bg-[var(--surface)] p-4 text-center">
      <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
        <PanelsTopLeft className="size-[1.15rem]" strokeWidth={1.7} />
      </span>
      <div>
        <div className="text-sm font-medium text-[var(--on-surface)]">{name}</div>
        <p className="mt-0.5 text-xs text-[var(--on-surface-variant)]">
          Open in its own window over this project
        </p>
      </div>
      <button
        type="button"
        onClick={() => void FocusNativeEditor(sessionId).catch(() => undefined)}
        className="flex h-8 items-center gap-1.5 rounded-full bg-[var(--primary)] px-4 text-xs font-semibold text-[var(--primary-foreground)] outline-none transition-[opacity,transform] hover:opacity-90 focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.97]"
      >
        Bring to front
      </button>
    </div>
  )
}

function isTerminalOutput(
  value: unknown,
): value is { sessionId: string; data: string } {
  return (
    isRecord(value) &&
    typeof value.sessionId === "string" &&
    typeof value.data === "string"
  )
}

function isTerminalExit(
  value: unknown,
): value is { sessionId: string; error: string } {
  return (
    isRecord(value) &&
    typeof value.sessionId === "string" &&
    typeof value.error === "string"
  )
}

function isEditorExit(
  value: unknown,
): value is { sessionId: string; exitCode: number; error: string; stopped?: boolean } {
  return (
    isRecord(value) &&
    typeof value.sessionId === "string" &&
    typeof value.exitCode === "number" &&
    typeof value.error === "string"
  )
}

function editorExitMessage(
  value: { exitCode: number; error: string; stopped?: boolean },
  title: string,
) {
  if (value.stopped) {
    return `${title} stopped along with the project. Retry to open it again.`
  }
  return value.error
    ? `${title} closed: ${value.error}`
    : `${title} closed (code ${value.exitCode}). Retry to open it again.`
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

const firstRunHintKey = "seizen-board-hint-dismissed"

// Human phrases for the assistant activity strip; jargon-free by design.
function assistantPhrase(tool: string): string | null {
  if (tool === "seizen_project_context" || tool === "") return null
  if (tool === "seizen_desk_open") return "The assistant opened something on your board"
  if (tool === "seizen_desk_add_note") return "The assistant added a note"
  if (tool === "seizen_desk_add_todo") return "The assistant added a checklist"
  if (tool === "seizen_desk_tidy") return "The assistant tidied your board"
  if (tool === "seizen_files_list") return "The assistant looked through your files"
  if (tool === "seizen_files_move") return "The assistant moved a file"
  if (tool === "seizen_files_rename") return "The assistant renamed a file"
  if (tool.startsWith("seizen_experiment_")) return "The assistant is working on an experiment"
  if (tool.startsWith("seizen_app_")) return "The assistant is working on the app"
  if (tool.startsWith("seizen_server_")) return "The assistant is working on a server"
  return null
}

const knownCommands = [
  "cmd", "wsl", "codex", "claude", "opencode", "browser", "navegador",
  "note", "nota", "todo", "tareas", "lista", "open", "abrir", "doc",
  "documento", "tidy", "ordenar", "folder", "carpeta", "help", "ayuda",
]

// Nearest-verb hint: prefix/containment first, then a cheap edit distance for
// one-typo cases ("noat" -> "nota"). Never a dead end.
function suggestCommand(input: string): string | undefined {
  if (!input) return undefined
  const exact = knownCommands.find(
    (candidate) => candidate.startsWith(input) || input.startsWith(candidate),
  )
  if (exact) return exact
  let best: string | undefined
  let bestDistance = 3
  for (const candidate of knownCommands) {
    if (Math.abs(candidate.length - input.length) >= bestDistance) continue
    const distance = editDistance(input, candidate, bestDistance)
    if (distance < bestDistance) {
      bestDistance = distance
      best = candidate
    }
  }
  return best
}

function editDistance(a: string, b: string, cap: number) {
  let previous = Array.from({ length: b.length + 1 }, (_, index) => index)
  for (let i = 1; i <= a.length; i++) {
    const current = [i]
    let rowMinimum = i
    for (let j = 1; j <= b.length; j++) {
      current[j] = Math.min(
        previous[j] + 1,
        current[j - 1] + 1,
        previous[j - 1] + (a[i - 1] === b[j - 1] ? 0 : 1),
      )
      rowMinimum = Math.min(rowMinimum, current[j])
    }
    if (rowMinimum >= cap) return cap
    previous = current
  }
  return previous[b.length]
}

function EmptyCanvasAction({
  icon: Icon,
  children,
  ...props
}: ComponentProps<"button"> & { icon: LucideIcon }) {
  return (
    <button
      type="button"
      className="flex h-9 items-center gap-2 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 text-xs font-medium text-[var(--on-surface)] shadow-[0_1px_3px_var(--shadow-soft)] outline-none transition-[transform,box-shadow,background-color] hover:-translate-y-0.5 hover:bg-[var(--surface-container)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.97] disabled:cursor-default disabled:opacity-35"
      {...props}
    >
      <Icon className="size-3.5 text-[var(--primary)]" strokeWidth={1.7} />
      {children}
    </button>
  )
}

const menuGroupOrderKey = "seizen-add-menu-order"
const defaultMenuGroupOrder = ["documents", "ai", "terminals", "editors", "tools"]

function readMenuGroupOrder(): string[] {
  try {
    const raw = localStorage.getItem(menuGroupOrderKey)
    if (!raw) return defaultMenuGroupOrder
    const parsed: unknown = JSON.parse(raw)
    if (!Array.isArray(parsed) || !parsed.every((key) => typeof key === "string")) {
      return defaultMenuGroupOrder
    }
    const known = parsed.filter((key) => defaultMenuGroupOrder.includes(key))
    return [
      ...known,
      ...defaultMenuGroupOrder.filter((key) => !known.includes(key)),
    ]
  } catch {
    return defaultMenuGroupOrder
  }
}

function orderMenuGroups<T extends { key: string }>(
  groups: T[],
  order: string[],
): T[] {
  return [...groups].sort((a, b) => {
    const indexA = order.indexOf(a.key)
    const indexB = order.indexOf(b.key)
    return (indexA === -1 ? order.length : indexA) -
      (indexB === -1 ? order.length : indexB)
  })
}

const regionTints: Record<NoteColor, string> = {
  default: "color-mix(in srgb, var(--on-surface) 4%, transparent)",
  amber: "color-mix(in srgb, #e4bd7d 10%, transparent)",
  emerald: "color-mix(in srgb, #9ccfb9 10%, transparent)",
  violet: "color-mix(in srgb, #c6b5ed 10%, transparent)",
  rose: "color-mix(in srgb, #e8a9b8 10%, transparent)",
}

// A canvas folder: a labeled rectangle that panels join by being dropped on it.
// Dragging its header carries every member panel along.
function RegionBox({
  region,
  hidden,
  memberCount,
  regionRef,
  onMove,
  onResize,
  onRename,
  onCycleColor,
  onSetFolder,
  onTerminalHere,
  onDissolve,
}: {
  region: StoredRegion
  hidden: boolean
  memberCount: number
  regionRef: (element: HTMLElement | null) => void
  onMove: (event: ReactPointerEvent<HTMLElement>) => void
  onResize: (event: ReactPointerEvent<HTMLButtonElement>) => void
  onRename: () => void
  onCycleColor: () => void
  onSetFolder: () => void
  onTerminalHere: () => void
  onDissolve: () => void
}) {
  const action =
    "flex size-6 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
  return (
    <section
      ref={regionRef}
      aria-label={`Folder ${region.label}`}
      className={cn(
        "pointer-events-auto absolute left-0 top-0 rounded-2xl border border-dashed border-[var(--outline-variant)]",
        hidden && "invisible",
      )}
      style={{
        translate: `${region.x}px ${region.y}px`,
        width: region.width,
        height: region.height,
        zIndex: 0,
        background: regionTints[region.color] ?? regionTints.default,
      }}
    >
      <div
        onPointerDown={onMove}
        className="group flex h-8 cursor-move items-center gap-1.5 px-2"
      >
        <FolderOpen className="size-3.5 shrink-0 text-[var(--on-surface-variant)]" strokeWidth={1.7} />
        <span className="min-w-0 truncate text-[0.68rem] font-semibold tracking-[-0.01em] text-[var(--on-surface)]">
          {region.label}
        </span>
        {region.cwd && (
          <span
            title={`New terminals start in ${region.cwd}`}
            className="min-w-0 truncate rounded-full bg-[var(--surface-container)] px-1.5 py-px text-[0.55rem] font-medium text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)]"
          >
            /{region.cwd}
          </span>
        )}
        <span className="text-[0.6rem] tabular-nums text-[var(--on-surface-variant)]">
          {memberCount > 0 ? memberCount : ""}
        </span>
        <span className="ml-auto flex items-center gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover:opacity-100">
          <Tooltip>
            <TooltipTrigger asChild>
              <button type="button" aria-label={`New terminal in ${region.label}`} onClick={onTerminalHere} className={action}>
                <SquareTerminal className="size-3.5" strokeWidth={1.7} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom">New terminal here</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <button type="button" aria-label={`Set folder location for ${region.label}`} onClick={onSetFolder} className={action}>
                <FolderOpen className="size-3.5" strokeWidth={1.7} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Folder location</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <button type="button" aria-label={`Change color of ${region.label}`} onClick={onCycleColor} className={action}>
                <Palette className="size-3.5" strokeWidth={1.7} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Color</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <button type="button" aria-label={`Rename ${region.label}`} onClick={onRename} className={action}>
                <Pencil className="size-3.5" strokeWidth={1.7} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Rename</TooltipContent>
          </Tooltip>
          <Tooltip>
            <TooltipTrigger asChild>
              <button
                type="button"
                aria-label={`Dissolve ${region.label}`}
                onClick={onDissolve}
                className={cn(action, "hover:bg-[var(--error)] hover:text-white")}
              >
                <X className="size-3.5" strokeWidth={1.9} />
              </button>
            </TooltipTrigger>
            <TooltipContent side="bottom">Dissolve</TooltipContent>
          </Tooltip>
        </span>
      </div>
      <button
        type="button"
        aria-label={`Resize folder ${region.label}`}
        title="Resize"
        onPointerDown={onResize}
        className="absolute bottom-0 right-0 size-6 cursor-se-resize rounded-tl-xl outline-none after:absolute after:bottom-1.5 after:right-1.5 after:size-2.5 after:border-b-2 after:border-r-2 after:border-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
      />
    </section>
  )
}

const noteSurfaces: Record<NoteColor, string> = {
  default: "var(--surface-container-high)",
  amber: "color-mix(in srgb, #e4bd7d 18%, var(--surface-container-high))",
  emerald: "color-mix(in srgb, #9ccfb9 18%, var(--surface-container-high))",
  violet: "color-mix(in srgb, #c6b5ed 18%, var(--surface-container-high))",
  rose: "color-mix(in srgb, #e8a9b8 18%, var(--surface-container-high))",
}

// Tiny markdown renderer for note view mode: headings, lists, checkboxes, bold,
// italic, and inline code. Input is escaped first, so nothing executes.
function renderNoteMarkdown(text: string) {
  const escaped = text
    .replace(/&/g, "&amp;")
    .replace(/</g, "&lt;")
    .replace(/>/g, "&gt;")
  const inline = (line: string) =>
    line
      .replace(/`([^`]+)`/g, "<code>$1</code>")
      .replace(/\*\*([^*]+)\*\*/g, "<strong>$1</strong>")
      .replace(/\*([^*]+)\*/g, "<em>$1</em>")
  const lines = escaped.split("\n").map((line) => {
    if (line.startsWith("### ")) return `<h3>${inline(line.slice(4))}</h3>`
    if (line.startsWith("## ")) return `<h2>${inline(line.slice(3))}</h2>`
    if (line.startsWith("# ")) return `<h1>${inline(line.slice(2))}</h1>`
    const task = /^[-*] \[([ xX])\] (.*)$/.exec(line)
    if (task) {
      const checked = task[1] !== " "
      return `<p class="note-task${checked ? " note-task-done" : ""}">${
        checked ? "☑" : "☐"
      } ${inline(task[2])}</p>`
    }
    if (line.startsWith("- ") || line.startsWith("* ")) {
      return `<p class="note-bullet">• ${inline(line.slice(2))}</p>`
    }
    if (!line.trim()) return "<br/>"
    return `<p>${inline(line)}</p>`
  })
  return lines.join("")
}

function NotePanel({
  node,
  onChange,
  onCycleColor,
}: {
  node: NoteNode
  onChange: (text: string) => void
  onCycleColor: () => void
}) {
  const [editing, setEditing] = useState(() => node.text.trim() === "")

  return (
    <div
      className="flex size-full min-h-0 flex-col"
      style={{ background: noteSurfaces[node.color] ?? noteSurfaces.default }}
    >
      <div className="flex h-8 shrink-0 items-center justify-end gap-1 px-2">
        <Tooltip>
          <TooltipTrigger asChild>
            <button
              type="button"
              aria-label="Change note color"
              onClick={onCycleColor}
              className="flex size-6 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
            >
              <Palette className="size-3.5" strokeWidth={1.7} />
            </button>
          </TooltipTrigger>
          <TooltipContent side="bottom">Color</TooltipContent>
        </Tooltip>
        <button
          type="button"
          aria-pressed={!editing}
          onClick={() => setEditing((current) => !current)}
          className="flex h-6 items-center rounded-full bg-[var(--surface-container)] px-2.5 text-[0.62rem] font-medium text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)] outline-none transition-colors hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
        >
          {editing ? "View" : "Edit"}
        </button>
      </div>
      {editing ? (
        <textarea
          value={node.text}
          maxLength={maximumNoteCharacters}
          onChange={(event) => onChange(event.target.value)}
          placeholder="Write a note… Markdown works: # heading, - list, - [ ] task"
          aria-label="Note text"
          className="min-h-0 flex-1 resize-none bg-transparent px-3 pb-3 text-sm leading-6 text-[var(--on-surface)] outline-none placeholder:text-[var(--on-surface-variant)]"
        />
      ) : (
        <div
          aria-label="Note preview"
          onDoubleClick={() => setEditing(true)}
          className="note-preview min-h-0 flex-1 select-text overflow-y-auto px-3 pb-3 text-sm leading-6"
          dangerouslySetInnerHTML={{ __html: renderNoteMarkdown(node.text) }}
        />
      )}
    </div>
  )
}

function TodoPanel({
  node,
  onChange,
}: {
  node: TodoNode
  onChange: (items: TodoItem[]) => void
}) {
  const [draft, setDraft] = useState("")
  const done = node.items.filter((item) => item.done).length

  const addItem = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const text = draft.trim().slice(0, maximumTodoItemCharacters)
    if (!text || node.items.length >= maximumTodoItems) return
    onChange([...node.items, { id: workspaceID("item"), text, done: false }])
    setDraft("")
  }

  return (
    <div className="flex size-full min-h-0 flex-col bg-[var(--surface-container-high)]">
      <div className="min-h-0 flex-1 overflow-y-auto px-2 py-2">
        {node.items.length === 0 && (
          <p className="px-2 py-3 text-center text-xs text-[var(--on-surface-variant)]">
            Nothing yet. Add your first task below.
          </p>
        )}
        {node.items.map((item) => (
          <div
            key={item.id}
            className="group flex items-center gap-2.5 rounded-xl px-2 py-1.5 transition-colors hover:bg-[var(--state-layer)]"
          >
            <input
              type="checkbox"
              checked={item.done}
              aria-label={item.text}
              onChange={() =>
                onChange(
                  node.items.map((candidate) =>
                    candidate.id === item.id
                      ? { ...candidate, done: !candidate.done }
                      : candidate,
                  ),
                )
              }
              className="size-4 shrink-0 accent-[var(--primary)]"
            />
            <span
              className={cn(
                "min-w-0 flex-1 break-words text-xs leading-5",
                item.done && "text-[var(--on-surface-variant)] line-through",
              )}
            >
              {item.text}
            </span>
            <button
              type="button"
              aria-label={`Remove ${item.text}`}
              onClick={() =>
                onChange(node.items.filter((candidate) => candidate.id !== item.id))
              }
              className="flex size-6 shrink-0 items-center justify-center rounded-full text-[var(--on-surface-variant)] opacity-0 outline-none transition-opacity hover:bg-[var(--error)] hover:text-white focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-[var(--ring)] group-hover:opacity-100"
            >
              <X className="size-3" strokeWidth={2} />
            </button>
          </div>
        ))}
      </div>
      <form
        onSubmit={addItem}
        className="flex h-11 shrink-0 items-center gap-2 border-t border-[var(--outline-variant)] px-3"
      >
        <input
          value={draft}
          maxLength={maximumTodoItemCharacters}
          onChange={(event) => setDraft(event.target.value)}
          placeholder="Add a task"
          aria-label="New task"
          className="h-8 min-w-0 flex-1 bg-transparent text-xs text-[var(--on-surface)] outline-none placeholder:text-[var(--on-surface-variant)]"
        />
        {node.items.length > 0 && (
          <span className="shrink-0 text-[0.62rem] tabular-nums text-[var(--on-surface-variant)]">
            {done}/{node.items.length}
          </span>
        )}
        <button
          type="submit"
          aria-label="Add task"
          disabled={!draft.trim() || node.items.length >= maximumTodoItems}
          className="flex size-7 shrink-0 items-center justify-center rounded-full text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-40 disabled:hover:bg-transparent"
        >
          <Plus className="size-4" strokeWidth={2} />
        </button>
      </form>
    </div>
  )
}

function workspaceID(prefix: string) {
  return typeof crypto !== "undefined" && "randomUUID" in crypto
    ? `${prefix}-${crypto.randomUUID()}`
    : `${prefix}-${Date.now()}-${Math.random().toString(16).slice(2)}`
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

export { ProjectWorkspace }
