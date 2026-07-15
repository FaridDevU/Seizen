import {
  useEffect,
  useRef,
  useState,
  type CSSProperties,
} from "react"
import {
  Check,
  CircleAlert,
  Copy,
  FileText,
  Folder,
  House,
  Library,
  Search,
  Server,
  Settings,
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
  isThemeAccent,
  SettingsPanel,
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
import type { GlobalServer } from "@/features/projects/project-service"
import { ServersView } from "@/features/servers/ServersView"
import { suspendAllWorkspaces } from "@/features/projects/workspace-lifecycle"
import { cn } from "@/lib/utils"

type NavId = "home" | "folders" | "servers" | "resources" | "settings"
type FilterId = "all" | "folders" | "resources" | "actions"
type EntryGroup = "folder" | "resource" | "navigation" | "action"

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

const resourceEntries: PaletteEntry[] = [
  {
    id: "resource-interface-guide",
    label: "Interface guide",
    description: "Reference document",
    group: "resource",
    icon: FileText,
    keywords: ["guide", "document", "interface", "resource"],
    target: "resources",
  },
  {
    id: "resource-visual-library",
    label: "Visual library",
    description: "128 resources",
    group: "resource",
    icon: Library,
    keywords: ["library", "icons", "images", "resource"],
    target: "resources",
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

const actionEntries: PaletteEntry[] = [
  {
    id: "action-copy-link",
    label: "Copy space link",
    description: "Save the address to the clipboard",
    group: "action",
    icon: Copy,
    keywords: ["copy", "link", "share", "url"],
    action: "copy-link",
  },
]

const filters: { id: FilterId; label: string }[] = [
  { id: "all", label: "All" },
  { id: "folders", label: "Folders" },
  { id: "resources", label: "Resources" },
  { id: "actions", label: "Actions" },
]

function App() {
  const [activeItem, setActiveItem] = useState<NavId>("folders")
  const [activeFilter, setActiveFilter] = useState<FilterId>("all")
  const [isDark, setIsDark] = useState(
    () => localStorage.getItem("seizen-theme") === "dark",
  )
  const [accent, setAccent] = useState<ThemeAccent>(() => {
    const cachedAccent = localStorage.getItem("seizen-accent")
    return isThemeAccent(cachedAccent) ? cachedAccent : "blue"
  })
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
  const isBrowsing = query.trim().length > 0 || activeFilter !== "all"
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
    let outcome: Feedback = {
      message: `${entry.label} opened`,
      tone: "success",
    }

    if (entry.target) {
      if (!(await navigateTo(entry.target))) {
        closePalette()
        return
      }
    }

    if (entry.action === "copy-link") {
      try {
        await navigator.clipboard.writeText(window.location.href)
        outcome.message = "Link copied"
      } catch {
        outcome = {
          message: "Could not copy the link",
          tone: "error",
        }
      }
    }

    closePalette()
    setFeedback(outcome)
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
            const isActive = activeItem === id

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
                      <Search className=”mx-auto mb-3 size-5 opacity-45” />
                      We found no results for “{query}”.
                    </CommandEmpty>

                    {!isBrowsing && (
                      <CommandGroup heading=”Recent”>
                        {[folderEntries[0], resourceEntries[0]].map(renderEntry)}
                      </CommandGroup>
                    )}

                    {!isBrowsing && <CommandSeparator />}

                    {(activeFilter === “all” || activeFilter === “folders”) &&
                      isBrowsing && (
                        <CommandGroup heading=”Folders”>
                          {folderEntries.map(renderEntry)}
                        </CommandGroup>
                      )}

                    {(activeFilter === “all” || activeFilter === “resources”) &&
                      isBrowsing && (
                        <CommandGroup heading=”Resources”>
                          {resourceEntries.map(renderEntry)}
                        </CommandGroup>
                      )}

                    {activeFilter === “all” && (
                      <CommandGroup heading=”Navigation”>
                        {navigationEntries.map(renderEntry)}
                      </CommandGroup>
                    )}

                    {(activeFilter === “all” || activeFilter === “actions”) && (
                      <CommandGroup heading=”Quick actions”>
                        {actionEntries.map(renderEntry)}
                      </CommandGroup>
                    )}
                  </CommandList>

                  <div className=”hidden items-center gap-4 border-t border-[var(--outline-variant)] px-4 py-2.5 text-[0.68rem] text-[var(--on-surface-variant)] sm:flex”>
                    <span><kbd>↑↓</kbd> navigate</span>
                    <span><kbd>↵</kbd> open</span>
                    <span className=”ml-auto”><kbd>Esc</kbd> close</span>
                  </div>
                </div>
              )}
            </Command>
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
            onOpenSettings={() => setActiveItem("settings")}
            onOpenCommandMenu={() => {
              setQuery("")
              setActiveFilter("all")
              setPaletteOpen(true)
            }}
          />
        </div>

        {activeItem === "servers" && <ServersView onOpen={openServer} />}

        {activeItem === "resources" && <ResourcesPanel />}

        {activeItem === "settings" && (
          <SettingsPanel
            isDark={isDark}
            accent={accent}
            onModeChange={(dark) => changeAppearance(dark, accent)}
            onAccentChange={(nextAccent) =>
              changeAppearance(isDark, nextAccent)
            }
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
