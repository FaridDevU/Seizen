import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from "react"
import {
  Check,
  CircleAlert,
  FileText,
  Folder,
  House,
  LayoutGrid,
  Library,
  ListChecks,
  Search,
  Server,
  Settings,
  SquareTerminal,
  StickyNote,
  X,
  type LucideIcon,
} from "lucide-react"

import seizenLogo from "../logo/logo (2).png"
import {
  CancelClose,
  ConfirmClose,
  GetAppearance,
  SetAppearance,
} from "../wailsjs/go/core/App"
import { EventsOn, WindowToggleMaximise } from "../wailsjs/runtime/runtime"
import { Button } from "@/components/ui/button"
import {
  isGlassPanel,
  isThemeAccent,
  SettingsPanel,
  type GlassPanel,
  type GlassTint,
  type ThemeAccent,
} from "@/components/SettingsPanel"
import { ConfirmHost } from "@/components/ui/confirm"
import { ResourcesPanel } from "@/components/ResourcesPanel"
import { WindowControls } from "@/components/WindowControls"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import {
  Tooltip,
  TooltipContent,
  TooltipProvider,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { ProjectLibrary } from "@/features/projects/ProjectLibrary"
import {
  projectService,
  type GlobalServer,
  type Project,
} from "@/features/projects/project-service"
import {
  queueQuickAction,
  requestOpenProject,
  requestWorkspaceAction,
  type WorkspaceQuickAction,
} from "@/features/projects/workspace-actions"
import { notifyInBackground } from "@/features/projects/notifications"
import { ServersView } from "@/features/servers/ServersView"
import { suspendAllWorkspaces } from "@/features/projects/workspace-lifecycle"
import { cn } from "@/lib/utils"

type NavId = "home" | "folders" | "servers" | "resources" | "settings"
type FilterId = "all" | "folders" | "actions"
type EntryGroup = "folder" | "resource" | "navigation" | "action"
type StartView = "home" | "folders"

type NavigationItem = {
  id: NavId
  label: string
  icon: LucideIcon
}

type PaletteEntry = {
  id: string
  label: string
  description: string
  group: EntryGroup
  icon: LucideIcon
  keywords: string[]
  target?: NavId
  action?: "copy-link"
}

type Feedback = {
  message: string
  tone: "success" | "error"
}

const navigation: NavigationItem[] = [
  { id: "home", label: "Home", icon: House },
  { id: "folders", label: "Folders", icon: Folder },
  { id: "servers", label: "Servers", icon: Server },
  { id: "resources", label: "Resources", icon: Library },
  { id: "settings", label: "Settings", icon: Settings },
]

const folderEntries: PaletteEntry[] = [
  {
    id: "folder-project-library",
    label: "Project library",
    description: "Create, import, and organize projects",
    group: "folder",
    icon: Folder,
    keywords: ["projects", "import", "git", "folders", "library"],
    target: "folders",
  },
]

const navigationEntries: PaletteEntry[] = navigation.map((item) => ({
  id: `nav-${item.id}`,
  label: item.label,
  description: "Switch section",
  group: "navigation",
  icon: item.icon,
  keywords: [item.id, item.label, "navigate", "open"],
  target: item.id,
}))

// Every quick action is deterministic: it works with no assistant and no API
// key, in the current workspace or by opening the most recent one.
const quickActions: Array<{
  id: string
  label: string
  description: string
  icon: LucideIcon
  keywords: string[]
  action: WorkspaceQuickAction
}> = [
  {
    id: "action-open-document",
    label: "Open document",
    description: "PDF, Word, image, or video on your board",
    icon: FileText,
    keywords: ["open", "document", "abrir", "documento", "pdf", "word", "docx"],
    action: "document",
  },
  {
    id: "action-new-note",
    label: "New note",
    description: "A sticky note on your board",
    icon: StickyNote,
    keywords: ["note", "nota", "write", "apuntar", "sticky"],
    action: "note",
  },
  {
    id: "action-new-todo",
    label: "New to-do list",
    description: "A checklist on your board",
    icon: ListChecks,
    keywords: ["todo", "tareas", "lista", "checklist", "tasks"],
    action: "todo",
  },
  {
    id: "action-tidy",
    label: "Tidy up the board",
    description: "Arrange every panel neatly",
    icon: LayoutGrid,
    keywords: ["tidy", "ordenar", "arrange", "organizar", "clean"],
    action: "tidy",
  },
  {
    id: "action-new-terminal",
    label: "New terminal",
    description: "A WSL terminal on your board",
    icon: SquareTerminal,
    keywords: ["terminal", "wsl", "shell", "consola"],
    action: "terminal",
  },
]

const filters: { id: FilterId; label: string }[] = [
  { id: "all", label: "All" },
  { id: "folders", label: "Spaces" },
  { id: "actions", label: "Actions" },
]

function formatRecentDate(value: string) {
  const date = new Date(value)
  if (Number.isNaN(date.getTime())) return ""
  const days = Math.floor((Date.now() - date.getTime()) / 86_400_000)
  if (days <= 0) return "Today"
  if (days === 1) return "Yesterday"
  if (days < 7) return `${days} days ago`
  return date.toLocaleDateString()
}

const startViewKey = "seizen-start-view"
const visitedKey = "seizen-visited"
const glassKey = "seizen-glass"
const glassTintKey = "seizen-glass-tint"
const glassLevelKey = "seizen-glass-level"
const wobblyKey = "seizen-wobbly"

function initialGlassPanels(): GlassPanel[] {
  try {
    const parsed: unknown = JSON.parse(localStorage.getItem(glassKey) ?? "[]")
    return Array.isArray(parsed) ? parsed.filter(isGlassPanel) : []
  } catch {
    return []
  }
}

function initialNavItem(): NavId {
  const preference = localStorage.getItem(startViewKey)
  if (preference === "home" || preference === "folders") return preference
  // Very first run lands on Home so a fresh install greets you with actions,
  // not an empty library; every later start keeps the classic library landing.
  if (!localStorage.getItem(visitedKey)) {
    localStorage.setItem(visitedKey, "1")
    return "home"
  }
  return "folders"
}

function App() {
  const [activeItem, setActiveItem] = useState<NavId>(initialNavItem)
  const [activeFilter, setActiveFilter] = useState<FilterId>("all")
  const [startView, setStartView] = useState<StartView>(() => {
    const preference = localStorage.getItem(startViewKey)
    return preference === "home" ? "home" : "folders"
  })
  const [recentProjects, setRecentProjects] = useState<Project[]>([])
  const [isDark, setIsDark] = useState(
    () => localStorage.getItem("seizen-theme") === "dark",
  )
  const [accent, setAccent] = useState<ThemeAccent>(() => {
    const cachedAccent = localStorage.getItem("seizen-accent")
    return isThemeAccent(cachedAccent) ? cachedAccent : "blue"
  })
  const [settingsOpen, setSettingsOpen] = useState(false)
  const [glassPanels, setGlassPanels] = useState<GlassPanel[]>(initialGlassPanels)
  const [glassTint, setGlassTint] = useState<GlassTint>(() =>
    localStorage.getItem(glassTintKey) === "light" ? "light" : "dark",
  )
  const [glassLevel, setGlassLevel] = useState<number>(() => {
    const parsed = Number(localStorage.getItem(glassLevelKey))
    return Number.isFinite(parsed) && parsed >= 0 && parsed <= 100 ? parsed : 50
  })
  const [wobbly, setWobbly] = useState(
    () => localStorage.getItem(wobblyKey) === "on",
  )
  const [paletteOpen, setPaletteOpen] = useState(false)
  const [query, setQuery] = useState("")
  const [feedback, setFeedback] = useState<Feedback | null>(null)
  const [gridSignal, setGridSignal] = useState(0)
  const [workspaceTarget, setWorkspaceTarget] = useState<{
    projectId: string
    serverId: string
  } | null>(null)
  const paletteRef = useRef<HTMLDivElement>(null)
  const inputRef = useRef<HTMLInputElement>(null)
  const appearanceGenerationRef = useRef(0)
  const appearanceSaveRef = useRef<Promise<void>>(Promise.resolve())
  const closingRef = useRef(false)

  // The assistant asking for approval is the one signal worth interrupting for
  // when the window is in the background.
  useEffect(
    () =>
      EventsOn("agent.approval.requested", () => {
        void notifyInBackground(
          "Seizen",
          "The assistant needs your approval to continue",
        )
      }),
    [],
  )

  useEffect(
    () =>
      EventsOn("seizen:before-close", () => {
        closingRef.current = true
        void Promise.all([
          suspendAllWorkspaces(),
          appearanceSaveRef.current,
        ])
          .then(() => ConfirmClose())
          .catch(() => {
            closingRef.current = false
            void CancelClose().catch(() => undefined)
          })
      }),
    [],
  )

  const activeIndex = navigation.findIndex((item) => item.id === activeItem)
  const shortcutLabel = /Mac|iPhone|iPad/.test(navigator.platform)
    ? "⌘K"
    : "Ctrl K"

  useEffect(() => {
    document.documentElement.classList.toggle("dark", isDark)
    document.documentElement.dataset.accent = accent
    localStorage.setItem("seizen-theme", isDark ? "dark" : "light")
    localStorage.setItem("seizen-accent", accent)

    document
      .querySelector<HTMLMetaElement>('meta[name="theme-color"]')
      ?.setAttribute("content", isDark ? "#171918" : "#f7f6f3")
  }, [accent, isDark])

  useEffect(() => {
    const root = document.documentElement
    root.dataset.glass = glassPanels.join(" ")
    root.style.setProperty(
      "--glass-tint",
      glassTint === "light" ? "#ffffff" : "#000000",
    )
    // The slider stores "how transparent"; CSS wants "how much tint".
    root.style.setProperty("--glass-opacity", `${100 - glassLevel}%`)
    // Blur fades out with transparency so the top of the slider is true glass.
    root.style.setProperty(
      "--glass-blur",
      `${Math.round((100 - glassLevel) * 0.2 * 10) / 10}px`,
    )
    localStorage.setItem(glassKey, JSON.stringify(glassPanels))
    localStorage.setItem(glassTintKey, glassTint)
    localStorage.setItem(glassLevelKey, String(glassLevel))
    root.dataset.wobbly = wobbly ? "on" : "off"
    localStorage.setItem(wobblyKey, wobbly ? "on" : "off")
  }, [glassPanels, glassTint, glassLevel, wobbly])

  useEffect(() => {
    let mounted = true
    const generation = appearanceGenerationRef.current

    void GetAppearance()
      .then((appearance) => {
        if (!mounted || generation !== appearanceGenerationRef.current) return
        setIsDark(appearance.mode === "dark")
        setAccent(isThemeAccent(appearance.accent) ? appearance.accent : "blue")
      })
      .catch((error: unknown) => {
        if (!mounted) return
        setFeedback({
          message: `Could not load appearance: ${String(error)}`,
          tone: "error",
        })
      })

    return () => {
      mounted = false
    }
  }, [])

  useEffect(() => {
    const handleShortcut = (event: KeyboardEvent) => {
      if (
        !event.repeat &&
        (event.metaKey || event.ctrlKey) &&
        event.key.toLowerCase() === "k"
      ) {
        event.preventDefault()
        setPaletteOpen((current) => {
          const next = !current
          if (!next) {
            setQuery("")
            setActiveFilter("all")
          }
          return next
        })
      }
    }

    window.addEventListener("keydown", handleShortcut)
    return () => window.removeEventListener("keydown", handleShortcut)
  }, [])

  useEffect(() => {
    if (!paletteOpen) return
    const frame = requestAnimationFrame(() => inputRef.current?.focus())
    return () => cancelAnimationFrame(frame)
  }, [paletteOpen])

  useEffect(() => {
    if (!paletteOpen) return

    const handleOutsidePress = (event: PointerEvent) => {
      if (!paletteRef.current?.contains(event.target as Node)) {
        setPaletteOpen(false)
        setQuery("")
        setActiveFilter("all")
      }
    }

    document.addEventListener("pointerdown", handleOutsidePress)
    return () => document.removeEventListener("pointerdown", handleOutsidePress)
  }, [paletteOpen])

  useEffect(() => {
    if (!feedback) return
    const timeout = window.setTimeout(() => setFeedback(null), 2400)
    return () => window.clearTimeout(timeout)
  }, [feedback])

  const closePalette = () => {
    setPaletteOpen(false)
    setQuery("")
    setActiveFilter("all")
  }

  const changeAppearance = (
    nextDark: boolean,
    nextAccent: ThemeAccent,
  ) => {
    if (
      closingRef.current ||
      (nextDark === isDark && nextAccent === accent)
    ) {
      return
    }

    const previousDark = isDark
    const previousAccent = accent
    const generation = ++appearanceGenerationRef.current

    setIsDark(nextDark)
    setAccent(nextAccent)

    const request = appearanceSaveRef.current
      .then(() => SetAppearance(nextDark ? "dark" : "light", nextAccent))
      .then(() => undefined)

    appearanceSaveRef.current = request.then(
      () => {
        if (generation !== appearanceGenerationRef.current) return
        setFeedback({ message: "Appearance saved", tone: "success" })
      },
      async (error: unknown) => {
        if (generation !== appearanceGenerationRef.current) return

        try {
          const saved = await GetAppearance()
          if (generation !== appearanceGenerationRef.current) return
          setIsDark(saved.mode === "dark")
          setAccent(isThemeAccent(saved.accent) ? saved.accent : "blue")
        } catch {
          if (generation !== appearanceGenerationRef.current) return
          setIsDark(previousDark)
          setAccent(previousAccent)
        }

        if (generation === appearanceGenerationRef.current) {
          setFeedback({
            message: `Could not save appearance: ${String(error)}`,
            tone: "error",
          })
        }
      },
    )
  }

  // Open workspaces stay alive while navigating; they only suspend when the app closes.
  const navigateTo = async (target: NavId) => {
    if (target === "settings") {
      setSettingsOpen(true)
      return true
    }
    if (target === "folders" && activeItem === "folders") {
      setGridSignal((current) => current + 1)
    }
    setActiveItem(target)
    return true
  }

  const openServer = async (server: GlobalServer) => {
    setWorkspaceTarget({
      projectId: server.projectId,
      serverId: server.id,
    })
    setActiveItem("folders")
  }

  const executeEntry = async (entry: PaletteEntry) => {
    if (entry.target) {
      if (!(await navigateTo(entry.target))) {
        closePalette()
        return
      }
    }
    closePalette()
    setFeedback({ message: `${entry.label} opened`, tone: "success" })
  }

  // Recents power both the Home view and the palette's Spaces group.
  const loadRecents = async () => {
    try {
      const projects = await projectService.list()
      setRecentProjects(
        projects
          .filter((project) => !project.archived)
          .sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : -1)),
      )
    } catch {
      // The palette still shows actions and navigation without recents.
    }
  }

  useEffect(() => {
    if (paletteOpen || activeItem === "home") void loadRecents()
  }, [paletteOpen, activeItem])

  const openProject = (project: Project) => {
    closePalette()
    setActiveItem("folders")
    requestOpenProject(project.id)
  }

  // Deterministic quick action: acts on the visible workspace, or opens the
  // most recent space with the action queued. Never a dead end.
  const runQuickAction = async (action: WorkspaceQuickAction) => {
    closePalette()
    setActiveItem("folders")
    // Wait two frames so the library (and its workspace) become visible first.
    await new Promise((resolve) =>
      requestAnimationFrame(() => requestAnimationFrame(resolve)),
    )
    if (requestWorkspaceAction(action)) return
    let recent = recentProjects[0]
    if (!recent) {
      await loadRecents()
      recent = (await projectService.list().catch(() => []))
        .filter((project) => !project.archived)
        .sort((a, b) => (a.updatedAt < b.updatedAt ? 1 : -1))[0]
    }
    if (!recent) {
      setFeedback({
        message: "Create a space first from the library",
        tone: "error",
      })
      return
    }
    queueQuickAction(action)
    requestOpenProject(recent.id)
  }

  const renderEntry = (entry: PaletteEntry) => {
    const Icon = entry.icon
    const label = entry.label

    return (
      <CommandItem
        key={entry.id}
        value={`${label} ${entry.description}`}
        keywords={entry.keywords}
        onSelect={() => void executeEntry(entry)}
      >
        <span className="flex size-8 items-center justify-center rounded-lg bg-[var(--surface-container)] text-[var(--on-surface-variant)]">
          <Icon strokeWidth={1.65} />
        </span>
        <span className="min-w-0 flex-1">
          <span className="block truncate font-medium">{label}</span>
          <span className="block truncate text-xs text-[var(--on-surface-variant)]">
            {entry.description}
          </span>
        </span>
      </CommandItem>
    )
  }

  return (
    <TooltipProvider>
      <main className="workspace-bg relative min-h-svh overflow-hidden text-[var(--on-surface)] transition-colors duration-300">
        <div className="ambient-rings" aria-hidden="true" />

        <header
          onDoubleClick={(event) => {
            if (!(event.target as HTMLElement).closest(".window-no-drag")) {
              WindowToggleMaximise()
            }
          }}
          className="window-drag fixed inset-x-0 top-0 z-30 flex items-center justify-between px-4 py-4 sm:px-6 sm:py-5 lg:px-8 lg:py-6 2xl:px-12 2xl:py-8"
        >
          <div className="flex items-center gap-4 text-[var(--on-surface)]">
            <span className="relative size-6 shrink-0 overflow-hidden" aria-hidden="true">
              <img
                src={seizenLogo}
                alt=""
                className="pointer-events-none absolute left-1/2 top-1/2 size-[2.35rem] max-w-none -translate-x-1/2 -translate-y-1/2 select-none object-contain"
              />
            </span>
            <span className="display-font text-[1.2rem] font-medium tracking-[0.075em]">
              Seizen
            </span>
          </div>

          <div className="window-no-drag">
            <WindowControls />
          </div>
        </header>

        <nav
          aria-label="Main navigation"
          style={{ "--active-index": activeIndex } as CSSProperties}
          className="fixed bottom-4 left-1/2 z-30 flex -translate-x-1/2 flex-row items-center gap-1 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container)] p-1.5 shadow-[0_1px_3px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated)] backdrop-blur-xl lg:bottom-auto lg:left-8 lg:top-1/2 lg:w-[3.625rem] lg:-translate-x-0 lg:-translate-y-1/2 lg:flex-col lg:gap-1 lg:px-2 lg:py-2 2xl:left-12 2xl:w-[3.875rem] 2xl:gap-1.5 2xl:py-2.5"
        >
          <span
            aria-hidden="true"
            className="nav-indicator absolute left-1.5 top-1.5 size-11 rounded-full bg-[var(--primary-container)] shadow-[0_1px_3px_var(--shadow-soft),inset_0_0_0_1px_var(--outline-variant)] lg:left-2 lg:top-2 lg:size-10 2xl:top-2.5 2xl:size-11"
          />

          {navigation.map(({ id, label, icon: Icon }) => {
            const isActive =
              id === "settings" ? settingsOpen : activeItem === id

            return (
              <Tooltip key={id}>
                <TooltipTrigger asChild>
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    aria-label={label}
                    aria-current={isActive ? "page" : undefined}
                    onClick={() => void navigateTo(id)}
                    className={cn(
                      "relative z-10 size-11 rounded-full text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] active:scale-[0.97] lg:size-10 2xl:size-11",
                      isActive &&
                        "text-[var(--on-primary-container)] hover:bg-transparent hover:text-[var(--on-primary-container)]",
                    )}
                  >
                    <Icon className="size-[1.12rem]" strokeWidth={1.65} />
                  </Button>
                </TooltipTrigger>
                <TooltipContent side="right">{label}</TooltipContent>
              </Tooltip>
            )
          })}
        </nav>

        {((activeItem !== "folders" &&
          activeItem !== "resources" &&
          activeItem !== "settings" &&
          activeItem !== "servers") ||
          paletteOpen) && (
          <section
            aria-label="Quick search"
            className="pointer-events-none absolute inset-0 z-[120] flex items-center justify-center px-4 pb-16 lg:pb-0"
          >
          <div
            ref={paletteRef}
            className={cn(
              "pointer-events-auto relative w-full max-w-[28rem] transition-transform duration-300 ease-[cubic-bezier(.22,1,.36,1)] sm:max-w-[29rem] 2xl:max-w-[32rem]",
              paletteOpen && "-translate-y-12 sm:-translate-y-10",
            )}
          >
            <Command
              loop
              label="Seizen commands"
              className="overflow-visible bg-transparent"
              onKeyDown={(event) => {
                if (event.key === "Escape") {
                  event.preventDefault()
                  closePalette()
                }
              }}
            >
              <div
                className={cn(
                  "flex h-[3.25rem] items-center gap-3 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-5 shadow-[0_1px_2px_var(--shadow-soft),0_8px_24px_var(--shadow-elevated)] backdrop-blur-xl transition-[border-color,box-shadow] duration-200 sm:h-14 2xl:h-[3.75rem] 2xl:px-[1.375rem]",
                  paletteOpen &&
                    "border-[var(--focus-border)] shadow-[0_1px_2px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated),0_0_0_3px_var(--focus-ring)]",
                )}
              >
                <Search
                  aria-hidden="true"
                  className="size-[1.08rem] shrink-0 text-[var(--primary)]"
                  strokeWidth={1.75}
                />

                {paletteOpen ? (
                  <CommandInput
                    ref={inputRef}
                    value={query}
                    onValueChange={setQuery}
                    aria-keyshortcuts="Meta+K Control+K"
                    placeholder="Search folders, resources, or actions..."
                  />
                ) : (
                  <input
                    ref={inputRef}
                    type="text"
                    role="combobox"
                    aria-label="Search project or run action"
                    aria-keyshortcuts="Meta+K Control+K"
                    aria-expanded="false"
                    aria-haspopup="listbox"
                    autoComplete="off"
                    placeholder="Search project or run action..."
                    className="h-full min-w-0 flex-1 bg-transparent text-sm outline-none placeholder:text-[var(--on-surface-variant)]"
                    onFocus={() => setPaletteOpen(true)}
                  />
                )}

                {query ? (
                  <Button
                    type="button"
                    variant="ghost"
                    size="icon"
                    className="size-8 rounded-full text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]"
                    aria-label="Clear search"
                    onMouseDown={(event) => event.preventDefault()}
                    onClick={() => {
                      setQuery("")
                      inputRef.current?.focus()
                    }}
                  >
                    <X className="size-4" strokeWidth={1.7} />
                  </Button>
                ) : (
                  <kbd className="flex h-7 min-w-10 shrink-0 items-center justify-center rounded-[0.6rem] bg-[var(--surface-container)] px-2.5 font-sans text-[0.68rem] font-medium tracking-[0.02em] text-[var(--on-surface-variant)] shadow-[inset_0_0_0_1px_var(--outline-variant)]">
                    {paletteOpen ? "Esc" : shortcutLabel}
                  </kbd>
                )}
              </div>

              {paletteOpen && (
                <div className="command-panel absolute inset-x-0 top-full mt-2.5 overflow-hidden rounded-[1.35rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_1px_3px_var(--shadow-soft),0_18px_44px_var(--shadow-elevated)] backdrop-blur-2xl">
                  <div
                    role="toolbar"
                    aria-label="Filter results"
                    className="flex items-center gap-1 overflow-x-auto border-b border-[var(--outline-variant)] px-3 py-2.5"
                  >
                    {filters.map((filter) => (
                      <Button
                        key={filter.id}
                        type="button"
                        variant="ghost"
                        aria-pressed={activeFilter === filter.id}
                        onMouseDown={(event) => event.preventDefault()}
                        onClick={() => {
                          setActiveFilter(filter.id)
                          inputRef.current?.focus()
                        }}
                        className={cn(
                          "h-7 rounded-full px-3 text-[0.7rem] font-medium text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]",
                          activeFilter === filter.id &&
                            "bg-[var(--primary-container)] text-[var(--on-primary-container)] hover:bg-[var(--primary-container)] hover:text-[var(--on-primary-container)]",
                        )}
                      >
                        {filter.label}
                      </Button>
                    ))}
                  </div>

                  <CommandList id="command-results" className="command-results">
                    <CommandEmpty>
                      <Search className="mx-auto mb-3 size-5 opacity-45" />
                      We found no results for "{query}".
                    </CommandEmpty>

                    {(activeFilter === "all" || activeFilter === "actions") && (
                      <CommandGroup heading="Do">
                        {quickActions.map((entry) => (
                          <CommandItem
                            key={entry.id}
                            value={`${entry.label} ${entry.description}`}
                            keywords={entry.keywords}
                            onSelect={() => void runQuickAction(entry.action)}
                          >
                            <span className="flex size-8 items-center justify-center rounded-lg bg-[var(--surface-container)] text-[var(--on-surface-variant)]">
                              <entry.icon strokeWidth={1.65} />
                            </span>
                            <span className="min-w-0 flex-1">
                              <span className="block truncate font-medium">
                                {entry.label}
                              </span>
                              <span className="block truncate text-xs text-[var(--on-surface-variant)]">
                                {entry.description}
                              </span>
                            </span>
                          </CommandItem>
                        ))}
                      </CommandGroup>
                    )}

                    {(activeFilter === "all" || activeFilter === "folders") &&
                      recentProjects.length > 0 && (
                        <CommandGroup heading="Spaces">
                          {recentProjects.slice(0, 8).map((project) => (
                            <CommandItem
                              key={project.id}
                              value={`${project.name} space project`}
                              keywords={[project.name, "space", "espacio", "open", "abrir"]}
                              onSelect={() => openProject(project)}
                            >
                              <span className="flex size-8 items-center justify-center rounded-lg bg-[var(--primary-container)] text-[0.62rem] font-semibold text-[var(--on-primary-container)]">
                                {project.name.slice(0, 2).toUpperCase()}
                              </span>
                              <span className="min-w-0 flex-1">
                                <span className="block truncate font-medium">
                                  {project.name}
                                </span>
                                <span className="block truncate text-xs text-[var(--on-surface-variant)]">
                                  Open this space
                                </span>
                              </span>
                            </CommandItem>
                          ))}
                        </CommandGroup>
                      )}

                    {(activeFilter === "all" || activeFilter === "folders") && (
                      <CommandGroup heading="Library">
                        {folderEntries.map(renderEntry)}
                      </CommandGroup>
                    )}

                    {activeFilter === "all" && (
                      <>
                        <CommandSeparator />
                        <CommandGroup heading="Navigation">
                          {navigationEntries.map(renderEntry)}
                        </CommandGroup>
                      </>
                    )}
                  </CommandList>

                  <div className="hidden items-center gap-4 border-t border-[var(--outline-variant)] px-4 py-2.5 text-[0.68rem] text-[var(--on-surface-variant)] sm:flex">
                    <span><kbd>↑↓</kbd> navigate</span>
                    <span><kbd>↵</kbd> open</span>
                    <span className="ml-auto"><kbd>Esc</kbd> close</span>
                  </div>
                </div>
              )}
            </Command>
          </div>
          </section>
        )}

        {activeItem === "home" && (
          <section
            aria-label="Home"
            className="view-enter absolute inset-x-0 bottom-0 top-[54%] z-10 overflow-y-auto px-4 pb-24 lg:pb-10"
          >
            <div className="mx-auto w-full max-w-[32rem]">
              <div className="flex flex-wrap items-center justify-center gap-2">
                {quickActions.map((entry) => (
                  <button
                    key={entry.id}
                    type="button"
                    onClick={() => void runQuickAction(entry.action)}
                    className="flex h-9 items-center gap-2 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 text-xs font-medium text-[var(--on-surface)] shadow-[0_1px_3px_var(--shadow-soft)] outline-none transition-[transform,box-shadow,background-color] hover:-translate-y-0.5 hover:bg-[var(--surface-container)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.97]"
                  >
                    <entry.icon className="size-3.5 text-[var(--primary)]" strokeWidth={1.7} />
                    {entry.label}
                  </button>
                ))}
              </div>

              {recentProjects.length > 0 && (
                <div className="mt-8">
                  <h2 className="px-1 text-[0.68rem] font-semibold uppercase tracking-[0.08em] text-[var(--on-surface-variant)]">
                    Recent
                  </h2>
                  <div className="mt-2 space-y-1">
                    {recentProjects.slice(0, 6).map((project) => (
                      <button
                        key={project.id}
                        type="button"
                        onClick={() => openProject(project)}
                        className="flex w-full items-center gap-3 rounded-2xl border border-transparent px-3 py-2.5 text-left outline-none transition-colors hover:border-[var(--outline-variant)] hover:bg-[var(--surface-container)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
                      >
                        <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[0.68rem] font-semibold text-[var(--on-primary-container)]">
                          {project.name.slice(0, 2).toUpperCase()}
                        </span>
                        <span className="min-w-0 flex-1">
                          <span className="block truncate text-sm font-medium">
                            {project.name}
                          </span>
                          <span className="block truncate text-xs text-[var(--on-surface-variant)]">
                            {formatRecentDate(project.updatedAt)}
                          </span>
                        </span>
                      </button>
                    ))}
                  </div>
                </div>
              )}
            </div>
          </section>
        )}

        <div className={cn(activeItem !== "folders" && "hidden")}>
          <ProjectLibrary
            initialProjectId={workspaceTarget?.projectId}
            initialMode={workspaceTarget ? "server-lab" : undefined}
            initialServerId={workspaceTarget?.serverId}
            onInitialWorkspaceConsumed={() => setWorkspaceTarget(null)}
            showGridSignal={gridSignal}
            onOpenSettings={() => setSettingsOpen(true)}
            onOpenCommandMenu={() => {
              setQuery("")
              setActiveFilter("all")
              setPaletteOpen(true)
            }}
          />
        </div>

        {activeItem === "servers" && <ServersView onOpen={openServer} />}

        {activeItem === "resources" && <ResourcesPanel />}

        {settingsOpen && (
          <SettingsPanel
            isDark={isDark}
            accent={accent}
            startView={startView}
            onModeChange={(dark) => changeAppearance(dark, accent)}
            onAccentChange={(nextAccent) =>
              changeAppearance(isDark, nextAccent)
            }
            onStartViewChange={(next) => {
              setStartView(next)
              localStorage.setItem(startViewKey, next)
              setFeedback({ message: "Start view saved", tone: "success" })
            }}
            glassPanels={glassPanels}
            onGlassToggle={(panel) =>
              setGlassPanels((current) =>
                current.includes(panel)
                  ? current.filter((item) => item !== panel)
                  : [...current, panel],
              )
            }
            glassTint={glassTint}
            onGlassTintChange={setGlassTint}
            glassLevel={glassLevel}
            onGlassLevelChange={setGlassLevel}
            wobbly={wobbly}
            onWobblyChange={setWobbly}
            onClose={() => setSettingsOpen(false)}
          />
        )}

        <ConfirmHost />

        {feedback && (
          <div
            key={`${feedback.tone}-${feedback.message}`}
            role={feedback.tone === "error" ? "alert" : "status"}
            aria-live={feedback.tone === "error" ? "assertive" : "polite"}
            style={{ "--toast-out-delay": "2150ms" } as CSSProperties}
            className="toast-auto-out fixed bottom-20 left-1/2 z-50 flex -translate-x-1/2 items-center gap-2 rounded-full border border-[var(--outline-variant)] bg-[var(--tooltip)] px-4 py-2 text-xs font-medium text-[var(--tooltip-foreground)] shadow-[0_8px_24px_var(--shadow-elevated)] lg:bottom-8"
          >
            {feedback.tone === "error" ? (
              <CircleAlert className="size-3.5 text-[var(--error)]" strokeWidth={2} />
            ) : (
              <Check className="size-3.5 text-[var(--primary)]" strokeWidth={2} />
            )}
            {feedback.message}
          </div>
        )}
      </main>
    </TooltipProvider>
  )
}

export default App
