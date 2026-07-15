import {
  useEffect,
  useMemo,
  useRef,
  useState,
  type ComponentProps,
  type FormEvent,
} from "react"
import * as DialogPrimitive from "@radix-ui/react-dialog"
import {
  Archive,
  Check,
  CircleAlert,
  CloudUpload,
  CopyPlus,
  Folder,
  FolderGit2,
  FolderInput,
  GitFork,
  Layers3,
  LoaderCircle,
  MoreHorizontal,
  Pencil,
  Play,
  Plus,
  RotateCcw,
  Search,
  Server,
  Square,
  Star,
  Trash2,
  Ungroup,
  X,
} from "lucide-react"

import { Button } from "@/components/ui/button"
import { confirmDialog } from "@/components/ui/confirm"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { suspendWorkspace } from "./workspace-lifecycle"

import {
  projectService,
  type DuplicateGroup,
  type GlobalServer,
  type Project,
  type ProjectFilter,
} from "./project-service"
import { ProjectWorkspace } from "./ProjectWorkspace"
import type { ProjectMode } from "./ProjectModeSelector"

type ModalState =
  | { kind: "create"; name: string }
  | { kind: "clone"; url: string }
  | { kind: "rename"; project: Project; name: string }
  | { kind: "github"; project: Project; url: string }
  | { kind: "backup"; project: Project }
  | { kind: "delete"; project: Project }

type Notice = { tone: "success" | "error"; message: string }
type LibraryItem =
  | { kind: "project"; project: Project }
  | { kind: "group"; id: string; title: string; projects: Project[] }

const filterOptions: { id: ProjectFilter; label: string }[] = [
  { id: "all", label: "All" },
  { id: "favorites", label: "Favorites" },
  { id: "archived", label: "Archived" },
]

const sourceLabels: Record<Project["source"], string> = {
  created: "Local",
  imported: "Imported",
  git: "GitHub",
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function isGitHubRemote(value: string | null | undefined) {
  if (!value || value !== value.trim()) return false
  const normalized = value.endsWith("/") ? value.slice(0, -1) : value
  let path = ""

  if (/^git@github\.com:/i.test(normalized)) {
    path = normalized.slice("git@github.com:".length)
  } else {
    try {
      const remote = new URL(normalized)
      if (remote.search || remote.hash) return false
      const https =
        remote.protocol === "https:" &&
        !remote.username &&
        !remote.password &&
        (!remote.port || remote.port === "443")
      const ssh =
        remote.protocol === "ssh:" &&
        remote.username === "git" &&
        !remote.password &&
        (!remote.port || remote.port === "22")
      if (
        remote.hostname.toLowerCase() !== "github.com" ||
        (!https && !ssh)
      ) {
        return false
      }
      path = remote.pathname.replace(/^\//, "")
    } catch {
      return false
    }
  }

  const segments = path.split("/")
  if (segments.length !== 2) return false
  const repository = segments[1].replace(/\.git$/i, "")
  return validGitHubSegment(segments[0]) && validGitHubSegment(repository)
}

function validGitHubSegment(value: string) {
  return value !== "." && value !== ".." && /^[a-z0-9_.-]+$/i.test(value)
}

type WorkspaceTarget = {
  project: Project
  initialMode?: ProjectMode
  initialServerId?: string
}

// ponytail: fixed cap on mounted workspaces; configurable only if someone asks for it
const MAX_LIVE_WORKSPACES = 3

function ProjectLibrary({
  onOpenSettings,
  onOpenCommandMenu,
  initialProjectId,
  initialMode,
  initialServerId,
  onInitialWorkspaceConsumed,
  showGridSignal = 0,
}: {
  onOpenSettings: () => void
  onOpenCommandMenu: () => void
  initialProjectId?: string
  initialMode?: ProjectMode
  initialServerId?: string
  onInitialWorkspaceConsumed?: () => void
  showGridSignal?: number
}) {
  const [projects, setProjects] = useState<Project[]>([])
  const [duplicates, setDuplicates] = useState<DuplicateGroup[]>([])
  const [thumbnails, setThumbnails] = useState<Record<string, string>>({})
  const [projectRoot, setProjectRoot] = useState("")
  const [openWorkspaces, setOpenWorkspaces] = useState<WorkspaceTarget[]>([])
  const [activeWorkspaceId, setActiveWorkspaceId] = useState<string | null>(null)
  // LRU order (first = most recent). Only these consume resources; the rest
  // stay suspended with their chip in the dock.
  const [liveWorkspaceIds, setLiveWorkspaceIds] = useState<string[]>([])
  const [servers, setServers] = useState<GlobalServer[]>([])
  const [query, setQuery] = useState("")
  const [filter, setFilter] = useState<ProjectFilter>("all")
  const [modal, setModal] = useState<ModalState | null>(null)
  const [notice, setNotice] = useState<Notice | null>(null)
  const [loading, setLoading] = useState(projectService.available)
  const [ready, setReady] = useState(false)
  const [initError, setInitError] = useState<string | null>(null)
  const [initAttempt, setInitAttempt] = useState(0)
  const [busy, setBusy] = useState<string | null>(null)
  const thumbnailCache = useRef(
    new Map<string, { version: string; thumbnail: string }>(),
  )
  const thumbnailGeneration = useRef(0)

  function openWorkspace(target: WorkspaceTarget) {
    setOpenWorkspaces((current) => {
      const exists = current.some((item) => item.project.id === target.project.id)
      if (!exists) return [...current, target]
      return current.map((item) =>
        item.project.id === target.project.id ? { ...item, ...target } : item,
      )
    })
    activateWorkspace(target.project.id)
  }

  function activateWorkspace(projectId: string) {
    setActiveWorkspaceId(projectId)
    setLiveWorkspaceIds((current) => [
      projectId,
      ...current.filter((id) => id !== projectId),
    ])
  }

  // Workspaces that exceed the cap are suspended first and only unmounted
  // afterward; unmounting first would leave orphaned processes without suspending them.
  useEffect(() => {
    const excess = liveWorkspaceIds.slice(MAX_LIVE_WORKSPACES)
    for (const projectId of excess) {
      void suspendWorkspace(projectId)
        .catch(() => undefined)
        .then(() => {
          setLiveWorkspaceIds((current) =>
            current.includes(projectId) && current.indexOf(projectId) >= MAX_LIVE_WORKSPACES
              ? current.filter((id) => id !== projectId)
              : current,
          )
        })
    }
  }, [liveWorkspaceIds])

  async function closeWorkspace(projectId: string) {
    try {
      await suspendWorkspace(projectId)
    } catch (error: unknown) {
      setNotice({ tone: "error", message: String(error) })
      return
    }
    releaseWorkspace(projectId)
    void refresh()
  }

  // Unmounts without suspending: for flows that already suspended (rename, delete).
  function releaseWorkspace(projectId: string) {
    setOpenWorkspaces((current) =>
      current.filter((item) => item.project.id !== projectId),
    )
    setLiveWorkspaceIds((current) => current.filter((id) => id !== projectId))
    setActiveWorkspaceId((current) => (current === projectId ? null : current))
  }

  const loadThumbnails = async (items: Project[], generation: number) => {
    // ponytail: sequential bridge calls avoid an unbounded burst; add a fixed
    // worker pool only if large libraries make thumbnail loading measurably slow.
    for (const project of items) {
      if (generation !== thumbnailGeneration.current) return
      const version = `${project.path}:${project.updatedAt}`
      if (thumbnailCache.current.get(project.id)?.version === version) continue

      try {
        const thumbnail = await projectService.getProjectThumbnail(project)
        if (generation !== thumbnailGeneration.current) return
        thumbnailCache.current.set(project.id, { version, thumbnail })
        setThumbnails((current) =>
          generation === thumbnailGeneration.current
            ? { ...current, [project.id]: thumbnail }
            : current,
        )
      } catch {
        // The clean fallback stays visible; transient errors retry next refresh.
      }
    }
  }

  const refresh = async () => {
    const generation = ++thumbnailGeneration.current
    const [currentProjects, archivedProjects, currentServers] = await Promise.all([
      projectService.list(),
      projectService.list("", "archived"),
      projectService.listAllServers().catch((caught: unknown) => {
        setNotice({
          tone: "error",
          message: `Could not load servers: ${String(caught)}`,
        })
        return []
      }),
    ])
    if (generation !== thumbnailGeneration.current) return
    const nextProjects = [...currentProjects, ...archivedProjects]
    const duplicateGroups = await projectService.detectDuplicates(
      nextProjects.filter(
        (project) => !project.archived && project.groupId === null,
      ),
    )
    if (generation !== thumbnailGeneration.current) return

    const currentIDs = new Set(nextProjects.map((project) => project.id))
    for (const id of thumbnailCache.current.keys()) {
      if (!currentIDs.has(id)) thumbnailCache.current.delete(id)
    }

    const currentThumbnails: Record<string, string> = {}
    for (const project of nextProjects) {
      const cached = thumbnailCache.current.get(project.id)
      const version = `${project.path}:${project.updatedAt}`
      if (cached?.version === version) {
        currentThumbnails[project.id] = cached.thumbnail
      } else if (cached) {
        thumbnailCache.current.delete(project.id)
      }
    }

    setProjects(nextProjects)
    setServers(currentServers ?? [])
    setDuplicates(duplicateGroups)
    setThumbnails(currentThumbnails)
    void loadThumbnails(nextProjects, generation)
  }

  useEffect(() => {
    if (!projectService.available) return

    let active = true
    const load = async () => {
      setLoading(true)
      setReady(false)
      setInitError(null)
      try {
        await projectService.initialize()
        if (!active) return
        const root = await projectService.getProjectRoot()
        if (!active) return
        setProjectRoot(root)
        await refresh()
        if (active) setReady(true)
      } catch (error) {
        if (active) setInitError(errorMessage(error))
      } finally {
        if (active) setLoading(false)
      }
    }

    void load()
    return () => {
      active = false
      thumbnailGeneration.current += 1
    }
  }, [initAttempt])

  useEffect(() => {
    if (!initialProjectId) return
    const project = projects.find((candidate) => candidate.id === initialProjectId)
    if (!project) return
    openWorkspace({ project, initialMode, initialServerId })
    onInitialWorkspaceConsumed?.()
  }, [
    initialMode,
    initialProjectId,
    initialServerId,
    onInitialWorkspaceConsumed,
    projects,
  ])

  useEffect(() => {
    if (showGridSignal > 0) setActiveWorkspaceId(null)
  }, [showGridSignal])

  useEffect(() => {
    if (!notice) return
    const timer = window.setTimeout(() => setNotice(null), 3600)
    return () => window.clearTimeout(timer)
  }, [notice])

  const visibleProjects = useMemo(() => {
    const term = query.trim().toLocaleLowerCase()

    return projects.filter((project) => {
      const matchesFilter =
        filter === "archived"
          ? project.archived
          : !project.archived &&
            (filter !== "favorites" || project.favorite)
      const matchesQuery =
        !term ||
        project.name.toLocaleLowerCase().includes(term) ||
        project.path.toLocaleLowerCase().includes(term) ||
        project.gitRemote?.toLocaleLowerCase().includes(term)

      return matchesFilter && matchesQuery
    })
  }, [filter, projects, query])

  const libraryItems = useMemo(() => {
    const items: LibraryItem[] = []
    const groups = new Map<string, Project[]>()

    for (const project of visibleProjects) {
      if (project.groupId) {
        const members = groups.get(project.groupId) ?? []
        members.push(project)
        groups.set(project.groupId, members)
      }
    }

    const renderedGroups = new Set<string>()
    for (const project of visibleProjects) {
      if (!project.groupId) {
        items.push({ kind: "project", project })
      } else if (!renderedGroups.has(project.groupId)) {
        items.push({
          kind: "group",
          id: project.groupId,
          title: project.groupTitle ?? project.name,
          projects: groups.get(project.groupId) ?? [project],
        })
        renderedGroups.add(project.groupId)
      }
    }

    return items
  }, [visibleProjects])

  const activeCount = projects.filter((project) => !project.archived).length
  const favoriteCount = projects.filter(
    (project) => project.favorite && !project.archived,
  ).length

  const run = async (key: string, action: () => Promise<void>) => {
    if (!ready) {
      setNotice({
        tone: "error",
        message: "The local library isn't available yet",
      })
      return
    }

    setBusy(key)
    try {
      await action()
    } catch (error) {
      setNotice({ tone: "error", message: errorMessage(error) })
    } finally {
      setBusy(null)
    }
  }

  const chooseProjectRoot = () =>
    void run("project-root", async () => {
      const path = await projectService.chooseDirectory(
        "Choose projects folder",
      )
      if (!path) return
      const root = await projectService.setProjectRoot(path)
      setProjectRoot(root)
      setNotice({ tone: "success", message: "Projects folder updated" })
    })

  const importProjects = () =>
    void run("import", async () => {
      const path = await projectService.chooseDirectory("Import project")
      if (!path) return
      const imported = await projectService.importFolders([path])
      await refresh()
      setNotice({
        tone: "success",
        message: `${imported.length} ${
          imported.length === 1 ? "project imported" : "projects imported"
        }`,
      })
    })

  const submitModal = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!modal) return

    void run(`modal-${modal.kind}`, async () => {
      if (modal.kind === "create") {
        await projectService.createProject(modal.name.trim())
        setNotice({ tone: "success", message: "Project created" })
      } else if (modal.kind === "clone") {
        await projectService.cloneRepository(modal.url.trim())
        setNotice({ tone: "success", message: "Repository cloned" })
      } else if (modal.kind === "rename") {
        await projectService.renameProject(modal.project, modal.name.trim())
        setNotice({ tone: "success", message: "Project renamed" })
      } else if (modal.kind === "github") {
        await projectService.setProjectGitHub(modal.project, modal.url.trim())
        setNotice({ tone: "success", message: "GitHub repository linked" })
      } else if (modal.kind === "backup") {
        if (!isGitHubRemote(modal.project.gitRemote)) {
          throw new Error("The project doesn't have a valid GitHub repository")
        }
        const message = await projectService.backupProject(modal.project)
        setNotice({ tone: "success", message })
      } else if (modal.project.source === "imported") {
        await projectService.removeProjectFromLibrary(modal.project)
        setNotice({ tone: "success", message: "Project removed from Seizen" })
      } else {
        await projectService.deleteProject(modal.project)
        setNotice({ tone: "success", message: "Project deleted" })
      }

      setModal(null)
      await refresh()
    })
  }

  const toggleFavorite = (project: Project) =>
    void run(`favorite-${project.id}`, async () => {
      await projectService.setFavorite(project, !project.favorite)
      await refresh()
    })

  const toggleArchive = (project: Project) =>
    void run(`archive-${project.id}`, async () => {
      await projectService.setArchived(project, !project.archived)
      await refresh()
      setNotice({
        tone: "success",
        message: project.archived ? "Project restored" : "Project archived",
      })
    })

  const groupDuplicate = (group: DuplicateGroup) =>
    void run(`group-${group.key}`, async () => {
      await projectService.groupDuplicate(group)
      await refresh()
      setNotice({ tone: "success", message: "Versions grouped" })
    })

  const ungroupVersions = async (groupId: string, title: string) => {
    const accepted = await confirmDialog({
      title: "Ungroup versions",
      message: `Projects in "${title}" will be listed separately again. Nothing is deleted.`,
      confirmLabel: "Ungroup",
    })
    if (!accepted) return
    void run(`ungroup-${groupId}`, async () => {
      await projectService.ungroupDuplicate(groupId)
      setNotice({ tone: "success", message: "Versions ungrouped" })
      await refresh()
    })
  }

  const openModal = (
    kind: "rename" | "github" | "backup" | "delete",
    project: Project,
  ) => {
    if (kind === "rename") {
      setModal({ kind, project, name: project.name })
    } else if (kind === "github") {
      const remote = project.gitRemote?.trim() ?? ""
      setModal({ kind, project, url: isGitHubRemote(remote) ? remote : "" })
    } else if (kind === "backup") {
      setModal({ kind, project })
    } else if (kind === "delete") {
      setModal({ kind, project })
    }
  }

  const openServer = (server: GlobalServer) => {
    const project = projects.find((candidate) => candidate.id === server.projectId)
    if (!project) {
      setNotice({ tone: "error", message: "Could not find the server's project" })
      return
    }
    openWorkspace({
      project,
      initialMode: "server-lab",
      initialServerId: server.id,
    })
  }

  if (!projectService.available) {
    return (
      <section className="absolute inset-0 flex items-center justify-center px-6 pb-20 pt-24 lg:pl-28">
        <div className="w-full max-w-md rounded-[1.4rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-7 text-center shadow-[0_12px_34px_var(--shadow-elevated)] backdrop-blur-xl">
          <span className="mx-auto flex size-11 items-center justify-center rounded-2xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
            <Folder className="size-5" strokeWidth={1.7} />
          </span>
          <h1 className="mt-5 text-lg font-semibold tracking-[-0.025em]">
            Open Seizen on the desktop
          </h1>
          <p className="mx-auto mt-2 max-w-sm text-sm leading-6 text-[var(--on-surface-variant)]">
            Your folders, Git, and the local library are only available in
            the desktop application.
          </p>
          <code className="mt-5 inline-flex rounded-lg bg-[var(--surface-container)] px-3 py-2 text-xs text-[var(--on-surface-variant)]">
            wails dev
          </code>
        </div>
      </section>
    )
  }

  return (
    <section className="view-enter absolute inset-0 overflow-y-auto px-4 pb-28 pt-24 sm:px-7 lg:pl-28 lg:pr-10 lg:pt-24 2xl:pl-36 2xl:pr-14 2xl:pt-28">
      <div className="mx-auto w-full max-w-[76rem]">
        <div className="flex flex-col gap-6 md:flex-row md:items-end md:justify-between">
          <div>
            <h1 className="display-font text-[2.15rem] font-light tracking-[-0.035em] sm:text-[2.6rem]">
              Local library
            </h1>
            <p className="mt-2 text-[0.8rem] font-medium tracking-[0.015em] text-[var(--on-surface-variant)]">
              {activeCount} active · {favoriteCount} favorites
            </p>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            <Button
              type="button"
              variant="outline"
              className="h-10 rounded-full border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 text-xs text-[var(--on-surface-variant)] shadow-[0_4px_14px_var(--shadow-soft)] hover:bg-[var(--surface-container)] hover:text-[var(--on-surface)]"
              disabled={!ready || busy !== null}
              onClick={importProjects}
            >
              {busy === "import" ? (
                <LoaderCircle className="size-4 animate-spin" />
              ) : (
                <FolderInput className="size-4" strokeWidth={1.7} />
              )}
              Import
            </Button>
            <Button
              type="button"
              className="h-11 rounded-full border border-[var(--focus-ring)] bg-[linear-gradient(135deg,var(--primary),var(--on-primary-container))] px-5 text-[0.78rem] font-semibold tracking-[0.01em] text-[var(--primary-foreground)] shadow-[0_8px_24px_var(--shadow-elevated)] hover:-translate-y-0.5 hover:brightness-[1.03] active:translate-y-0"
              disabled={!ready || busy !== null}
              onClick={() => setModal({ kind: "create", name: "" })}
            >
              <Plus className="size-4" strokeWidth={1.8} />
              New project
            </Button>
          </div>
        </div>

        <div className="mt-7 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="relative w-full sm:max-w-md">
            <Search
              aria-hidden="true"
              className="pointer-events-none absolute left-4 top-1/2 size-4 -translate-y-1/2 text-[var(--primary)]"
              strokeWidth={1.7}
            />
            <Input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              placeholder="Search projects"
              aria-label="Search projects"
              className="h-11 rounded-full border-[var(--focus-border)] bg-[var(--surface-container-high)] pl-11 pr-10 text-sm shadow-[0_6px_20px_var(--shadow-soft)] transition-[border-color,box-shadow] focus-visible:border-[var(--primary)] focus-visible:shadow-[0_8px_24px_var(--shadow-elevated)]"
            />
            {query && (
              <button
                type="button"
                aria-label="Clear search"
                onClick={() => setQuery("")}
                className="absolute right-2 top-1/2 flex size-6 -translate-y-1/2 items-center justify-center rounded-full text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)]"
              >
                <X className="size-3.5" />
              </button>
            )}
          </div>

          <div
            role="group"
            aria-label="Filter projects"
            className="flex w-fit items-center gap-1 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container)] p-1 shadow-[0_4px_16px_var(--shadow-soft)] backdrop-blur-xl"
          >
            {filterOptions.map((option) => (
              <button
                key={option.id}
                type="button"
                aria-pressed={filter === option.id}
                onClick={() => setFilter(option.id)}
                className={cn(
                  "h-8 rounded-full px-4 text-[0.72rem] font-medium text-[var(--on-surface-variant)] transition-[color,background-color,box-shadow] hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)]",
                  filter === option.id &&
                    "bg-[var(--primary-container)] text-[var(--on-primary-container)] shadow-[0_2px_8px_var(--shadow-soft)] hover:bg-[var(--primary-container)] hover:text-[var(--on-primary-container)]",
                )}
              >
                {option.label}
              </button>
            ))}
          </div>
        </div>

        {filter !== "archived" && duplicates.length > 0 && (
          <div className="mt-5 space-y-2">
            {duplicates.map((group) => (
              <div
                key={group.key}
                className="flex flex-col gap-4 rounded-2xl border border-[var(--focus-ring)] bg-[var(--primary-container)]/55 p-4 sm:flex-row sm:items-center"
              >
                <span className="flex size-9 shrink-0 items-center justify-center rounded-xl bg-[var(--surface-container-high)] text-[var(--on-primary-container)] shadow-[0_1px_3px_var(--shadow-soft)]">
                  <Layers3 className="size-4" strokeWidth={1.7} />
                </span>
                <div className="min-w-0 flex-1">
                  <p className="text-sm font-semibold tracking-[-0.015em]">
                    Are these versions of {group.title}?
                  </p>
                  <p className="mt-0.5 truncate text-xs text-[var(--on-surface-variant)]">
                    {group.variants.map((variant) => variant.label).join(" · ")}
                  </p>
                </div>
                <Button
                  type="button"
                  variant="outline"
                  className="h-8 rounded-xl border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-3 text-xs shadow-none"
                  disabled={!ready || busy !== null}
                  onClick={() => groupDuplicate(group)}
                >
                  {busy === `group-${group.key}` ? (
                    <LoaderCircle className="size-3.5 animate-spin" />
                  ) : (
                    <CopyPlus className="size-3.5" strokeWidth={1.7} />
                  )}
                  Group versions
                </Button>
              </div>
            ))}
          </div>
        )}

        {filter === "all" && !query && servers.length > 0 && (
          <section
            aria-labelledby="library-servers-title"
            className="mt-5 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 py-3 shadow-[0_1px_3px_var(--shadow-soft)]"
          >
            <div className="mb-2 flex items-center justify-between gap-3">
              <h2 id="library-servers-title" className="text-xs font-semibold">
                Servers
              </h2>
              {servers.length > 3 && (
                <span className="text-[0.65rem] text-[var(--on-surface-variant)]">
                  +{servers.length - 3} in the global view
                </span>
              )}
            </div>
            <div className="divide-y divide-[var(--outline-variant)]">
              {servers.slice(0, 3).map((server) => {
                const changing = [
                  "provisioning",
                  "starting",
                  "stopping",
                  "deleting",
                ].includes(server.status)
                const active = ["running", "degraded"].includes(server.status)
                const serverBusy = busy?.endsWith(server.id) ?? false

                return (
                  <div
                    key={server.id}
                    className="flex min-w-0 flex-wrap items-center gap-2 py-2 first:pt-0 last:pb-0"
                  >
                    <Server className="size-3.5 shrink-0 text-[var(--primary)]" />
                    <div className="min-w-0 flex-1">
                      <p className="truncate text-xs font-medium">{server.name}</p>
                      <p className="truncate text-[0.62rem] text-[var(--on-surface-variant)]">
                        {server.projectName ??
                          projects.find((project) => project.id === server.projectId)
                            ?.name ??
                          server.projectId} · {server.cpuLimit} CPU ·{" "}
                        {server.memoryMb} MB RAM
                      </p>
                    </div>
                    {active ? (
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={busy !== null || changing}
                        onClick={() =>
                          void run(`server-stop-${server.id}`, async () => {
                            await projectService.stopServer(server.id)
                            await refresh()
                          })
                        }
                        className="h-7 rounded-full px-2.5 text-[0.68rem]"
                      >
                        {serverBusy ? (
                          <LoaderCircle className="size-3 animate-spin" />
                        ) : (
                          <Square className="size-3" />
                        )}
                        Stop
                      </Button>
                    ) : (
                      <Button
                        type="button"
                        variant="ghost"
                        disabled={
                          busy !== null ||
                          changing ||
                          !["stopped", "failed"].includes(server.status)
                        }
                        onClick={() =>
                          void run(`server-start-${server.id}`, async () => {
                            await projectService.startServer(server.id)
                            await refresh()
                          })
                        }
                        className="h-7 rounded-full px-2.5 text-[0.68rem]"
                      >
                        {serverBusy ? (
                          <LoaderCircle className="size-3 animate-spin" />
                        ) : (
                          <Play className="size-3" />
                        )}
                        Start
                      </Button>
                    )}
                    <Button
                      type="button"
                      variant="outline"
                      disabled={busy !== null}
                      onClick={() => openServer(server)}
                      className="h-7 rounded-full border-[var(--outline-variant)] px-2.5 text-[0.68rem] shadow-none"
                    >
                      Open
                    </Button>
                  </div>
                )
              })}
            </div>
          </section>
        )}

        <div className="mt-4">
          {loading ? (
            <div className="flex min-h-56 items-center justify-center gap-2 rounded-2xl bg-[var(--surface-container-high)] text-sm text-[var(--on-surface-variant)]">
              <LoaderCircle className="size-4 animate-spin" />
              Loading projects
            </div>
          ) : initError ? (
            <div className="flex min-h-64 flex-col items-center justify-center rounded-2xl bg-[var(--surface-container-high)] px-6 text-center">
              <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--surface-container)] text-[var(--error)]">
                <CircleAlert className="size-[1.1rem]" strokeWidth={1.7} />
              </span>
              <p className="mt-4 text-sm font-semibold">
                We couldn't open the library
              </p>
              <p className="mt-1 max-w-sm text-xs leading-5 text-[var(--on-surface-variant)]">
                {initError}
              </p>
              <Button
                type="button"
                variant="outline"
                className="mt-4 h-8 rounded-xl px-3 text-xs shadow-none"
                onClick={() => setInitAttempt((attempt) => attempt + 1)}
              >
                <RotateCcw className="size-3.5" />
                Retry
              </Button>
            </div>
          ) : visibleProjects.length === 0 ? (
            <div className="flex min-h-64 flex-col items-center justify-center rounded-2xl bg-[var(--surface-container-high)] px-6 text-center">
              <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--surface-container)] text-[var(--on-surface-variant)]">
                <Folder className="size-[1.15rem]" strokeWidth={1.6} />
              </span>
              <p className="mt-4 text-sm font-semibold">
                {query ? "We couldn't find that project" : "Your library is ready"}
              </p>
              <p className="mt-1 max-w-xs text-xs leading-5 text-[var(--on-surface-variant)]">
                {query
                  ? "Try a different name, path, or repository."
                  : filter === "archived"
                    ? "Projects you archive will appear here."
                    : "Create, import a folder, or clone your first repository."}
              </p>
            </div>
          ) : (
            <div className="space-y-2">
              {libraryItems.map((item) =>
                item.kind === "project" ? (
                  <ProjectRow
                    key={item.project.id}
                    project={item.project}
                    thumbnail={thumbnails[item.project.id] ?? ""}
                    busy={busy}
                    onOpen={() => openWorkspace({ project: item.project })}
                    onFavorite={() => toggleFavorite(item.project)}
                    onRename={() => openModal("rename", item.project)}
                    onGitHub={() => openModal("github", item.project)}
                    onBackup={() => openModal("backup", item.project)}
                    onArchive={() => toggleArchive(item.project)}
                    onDelete={() => openModal("delete", item.project)}
                  />
                ) : (
                  <div
                    key={item.id}
                    className="relative rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_1px_3px_var(--shadow-soft)]"
                  >
                    <div className="flex items-center gap-3 rounded-t-2xl bg-[var(--surface-container)] px-4 py-3">
                      <span className="flex size-8 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]">
                        <Layers3 className="size-4" strokeWidth={1.7} />
                      </span>
                      <div className="min-w-0 flex-1">
                        <p className="truncate text-sm font-semibold tracking-[-0.02em]">
                          {item.title}
                        </p>
                        <p className="text-[0.68rem] text-[var(--on-surface-variant)]">
                          {item.projects.length} related versions
                        </p>
                      </div>
                      <IconAction
                        label="Ungroup versions"
                        disabled={busy !== null}
                        onClick={() => void ungroupVersions(item.id, item.title)}
                      >
                        <Ungroup className="size-4" strokeWidth={1.7} />
                      </IconAction>
                    </div>
                    <div className="divide-y divide-[var(--outline-variant)] border-t border-[var(--outline-variant)]">
                      {item.projects.map((project) => (
                        <ProjectRow
                          key={project.id}
                          project={project}
                          thumbnail={thumbnails[project.id] ?? ""}
                          busy={busy}
                          nested
                          onOpen={() => openWorkspace({ project })}
                          onFavorite={() => toggleFavorite(project)}
                          onRename={() => openModal("rename", project)}
                          onGitHub={() => openModal("github", project)}
                          onBackup={() => openModal("backup", project)}
                          onArchive={() => toggleArchive(project)}
                          onDelete={() => openModal("delete", project)}
                        />
                      ))}
                    </div>
                  </div>
                ),
              )}
            </div>
          )}
        </div>
      </div>

      {modal && (
        <DialogPrimitive.Root
          open
          onOpenChange={(open) => {
            if (!open && !busy) setModal(null)
          }}
        >
          <DialogPrimitive.Portal>
            <DialogPrimitive.Overlay className="overlay-in fixed inset-0 z-50 bg-black/20 backdrop-blur-[3px] dark:bg-black/45" />
            <DialogPrimitive.Content asChild>
              <form
                aria-labelledby="project-dialog-title"
                aria-busy={busy === "modal-backup"}
                onSubmit={submitModal}
                className="dialog-in fixed left-1/2 top-1/2 z-50 w-[calc(100%-2rem)] max-w-[27rem] rounded-[1.4rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_22px_60px_var(--shadow-elevated)] outline-none"
              >
                <div className="flex items-start justify-between gap-4">
                  <div>
                    <DialogPrimitive.Title asChild>
                      <p
                        id="project-dialog-title"
                        className="text-base font-semibold tracking-[-0.025em]"
                      >
                        {modal.kind === "rename"
                          ? "Rename project"
                          : modal.kind === "github"
                            ? isGitHubRemote(modal.project.gitRemote)
                              ? "Change repository"
                              : "Link to GitHub"
                            : modal.kind === "backup"
                              ? "Create backup"
                              : modal.kind === "delete"
                                ? modal.project.source === "imported"
                                  ? "Remove from Seizen"
                                  : "Delete project"
                                : "New project"}
                      </p>
                    </DialogPrimitive.Title>
                    <DialogPrimitive.Description asChild>
                      <p className="mt-1 text-xs leading-5 text-[var(--on-surface-variant)]">
                        {modal.kind === "rename"
                          ? "We'll also rename the folder on disk."
                          : modal.kind === "github"
                            ? `Connect ${modal.project.name} to a GitHub repository.`
                            : modal.kind === "backup"
                              ? "Review the destination before confirming the commit and push."
                              : modal.kind === "delete"
                                ? modal.project.source === "imported"
                                  ? "The project will leave the Seizen library. Your folder on disk won't be touched."
                                  : "The folder and all its contents will be permanently deleted. This action cannot be undone."
                                : "Create a local folder or clone a GitHub repository."}
                      </p>
                    </DialogPrimitive.Description>
                  </div>
                  <button
                    type="button"
                    aria-label="Close"
                    disabled={busy !== null}
                    onClick={() => setModal(null)}
                    className="flex size-8 shrink-0 items-center justify-center rounded-full text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)] disabled:opacity-50"
                  >
                    <X className="size-4" />
                  </button>
                </div>

                <div className="mt-5 space-y-4">
                  {(modal.kind === "github" ||
                    modal.kind === "backup" ||
                    modal.kind === "delete") && (
                    <ProjectIdentity
                      project={modal.project}
                      destructive={modal.kind === "delete"}
                    />
                  )}

                  {modal.kind === "backup" && (
                    <div className="rounded-xl border border-[var(--focus-ring)] bg-[var(--primary-container)]/55 p-3 text-xs">
                      <p className="font-medium">Destination</p>
                      <p
                        className="mt-1 truncate text-[0.68rem] text-[var(--on-surface-variant)]"
                        title={modal.project.gitRemote ?? ""}
                      >
                        {modal.project.gitRemote}
                      </p>
                      <p className="mt-2 leading-5 text-[var(--on-surface-variant)]">
                        All non-ignored files in the project will be included.
                      </p>
                    </div>
                  )}

                  {(modal.kind === "create" || modal.kind === "clone") && (
                    <div
                      role="group"
                      className="grid grid-cols-2 gap-2"
                      aria-label="Project type"
                    >
                      <button
                        type="button"
                        aria-pressed={modal.kind === "create"}
                        disabled={busy !== null}
                        onClick={() => setModal({ kind: "create", name: "" })}
                        className={cn(
                          "flex items-center justify-center gap-2 rounded-xl border border-[var(--outline-variant)] px-3 py-2.5 text-xs font-medium text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                          modal.kind === "create" &&
                            "border-[var(--focus-border)] bg-[var(--primary-container)] text-[var(--on-primary-container)]",
                        )}
                      >
                        <Folder className="size-4" strokeWidth={1.7} />
                        Local project
                      </button>
                      <button
                        type="button"
                        aria-pressed={modal.kind === "clone"}
                        disabled={busy !== null}
                        onClick={() => setModal({ kind: "clone", url: "" })}
                        className={cn(
                          "flex items-center justify-center gap-2 rounded-xl border border-[var(--outline-variant)] px-3 py-2.5 text-xs font-medium text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                          modal.kind === "clone" &&
                            "border-[var(--focus-border)] bg-[var(--primary-container)] text-[var(--on-primary-container)]",
                        )}
                      >
                        <FolderGit2 className="size-4" strokeWidth={1.7} />
                        From GitHub
                      </button>
                    </div>
                  )}

                  {(modal.kind === "create" || modal.kind === "rename") && (
                    <label className="block space-y-1.5 text-xs font-medium">
                      Name
                      <Input
                        autoFocus
                        required
                        value={modal.name}
                        placeholder="My project"
                        onChange={(event) =>
                          setModal({ ...modal, name: event.target.value })
                        }
                        className="h-10 rounded-xl border-[var(--outline-variant)] bg-[var(--surface-container)] text-xs shadow-none"
                      />
                    </label>
                  )}

                  {(modal.kind === "clone" || modal.kind === "github") && (
                    <label className="block space-y-1.5 text-xs font-medium">
                      GitHub URL
                      <Input
                        autoFocus
                        required
                        value={modal.url}
                        placeholder="https://github.com/user/project.git"
                        onChange={(event) =>
                          setModal({ ...modal, url: event.target.value })
                        }
                        className="h-10 rounded-xl border-[var(--outline-variant)] bg-[var(--surface-container)] text-xs shadow-none"
                      />
                    </label>
                  )}

                  {(modal.kind === "create" || modal.kind === "clone") && (
                    <div>
                      <span className="text-xs font-medium">
                        Projects folder
                      </span>
                      <button
                        type="button"
                        aria-label={
                          projectRoot
                            ? `Change projects folder. Current location: ${projectRoot}`
                            : "Choose projects folder"
                        }
                        disabled={busy !== null}
                        onClick={chooseProjectRoot}
                        className="mt-1.5 flex h-11 w-full min-w-0 items-center gap-2.5 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] px-3 text-left text-xs outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-60"
                      >
                        <Folder
                          className="size-4 shrink-0 text-[var(--primary)]"
                          strokeWidth={1.7}
                        />
                        <span
                          className={cn(
                            "min-w-0 flex-1 truncate",
                            !projectRoot && "text-[var(--on-surface-variant)]",
                          )}
                          title={projectRoot}
                        >
                          {projectRoot || "Choose a folder"}
                        </span>
                        {busy === "project-root" ? (
                          <LoaderCircle className="size-3.5 shrink-0 animate-spin text-[var(--on-surface-variant)]" />
                        ) : (
                          <span className="shrink-0 text-[0.68rem] font-medium text-[var(--primary)]">
                            Change
                          </span>
                        )}
                      </button>
                    </div>
                  )}
                </div>

                <div className="mt-6 flex justify-end gap-2">
                  <Button
                    type="button"
                    variant="ghost"
                    className="h-9 rounded-xl px-3 text-xs"
                    autoFocus={
                      modal.kind === "backup" || modal.kind === "delete"
                    }
                    disabled={busy !== null}
                    onClick={() => setModal(null)}
                  >
                    Cancel
                  </Button>
                  <Button
                    type="submit"
                    className={cn(
                      "h-9 rounded-xl px-4 text-xs",
                      modal.kind === "delete" &&
                        "bg-[var(--error)] text-white hover:bg-[var(--error)] hover:brightness-95 focus-visible:ring-[var(--error)] dark:text-[#271817]",
                    )}
                    disabled={
                      busy !== null ||
                      (modal.kind === "create" &&
                        (!modal.name.trim() || !projectRoot)) ||
                      (modal.kind === "clone" &&
                        (!modal.url.trim() || !projectRoot)) ||
                      (modal.kind === "rename" && !modal.name.trim()) ||
                      (modal.kind === "github" && !modal.url.trim()) ||
                      (modal.kind === "backup" &&
                        !isGitHubRemote(modal.project.gitRemote))
                    }
                  >
                    {busy === `modal-${modal.kind}` && (
                      <LoaderCircle className="size-3.5 animate-spin" />
                    )}
                    {modal.kind === "create"
                      ? "Create project"
                      : modal.kind === "clone"
                        ? "Clone repository"
                        : modal.kind === "rename"
                          ? "Save name"
                          : modal.kind === "github"
                            ? "Save link"
                            : modal.kind === "backup"
                              ? busy === "modal-backup"
                                ? "Backing up..."
                                : "Confirm backup"
                              : "Delete forever"}
                  </Button>
                </div>
                {modal.kind === "backup" && (
                  <span
                    role="status"
                    aria-live="polite"
                    aria-atomic="true"
                    className="sr-only"
                  >
                    {busy === "modal-backup" ? "Creating backup" : ""}
                  </span>
                )}
              </form>
            </DialogPrimitive.Content>
          </DialogPrimitive.Portal>
        </DialogPrimitive.Root>
      )}

      {notice && (
        <div
          key={`${notice.tone}-${notice.message}`}
          role={notice.tone === "error" ? "alert" : "status"}
          aria-live={notice.tone === "error" ? "assertive" : "polite"}
          className="toast-auto-out fixed bottom-20 left-1/2 z-[60] flex -translate-x-1/2 items-center gap-2 rounded-full bg-[var(--tooltip)] px-4 py-2 text-xs font-medium text-[var(--tooltip-foreground)] shadow-[0_8px_24px_var(--shadow-elevated)] lg:bottom-8"
        >
          {notice.tone === "error" ? (
            <CircleAlert className="size-3.5 text-[var(--error)]" />
          ) : (
            <Check className="size-3.5 text-[var(--primary)]" />
          )}
          <span
            className="max-w-[min(32rem,72vw)] truncate"
            title={notice.message}
          >
            {notice.message}
          </span>
        </div>
      )}

      {openWorkspaces
        .filter((target) => liveWorkspaceIds.includes(target.project.id))
        .map((target) => (
        <div
          key={target.project.id}
          className={cn(
            "absolute inset-0",
            activeWorkspaceId !== target.project.id && "hidden",
          )}
        >
          <ProjectWorkspace
            project={target.project}
            initialMode={target.initialMode}
            initialServerId={target.initialServerId}
            dock={{
              items: openWorkspaces.map((item) => ({
                id: item.project.id,
                name: item.project.name,
                thumbnail: thumbnails[item.project.id] || undefined,
                live: liveWorkspaceIds.includes(item.project.id),
                active: activeWorkspaceId === item.project.id,
              })),
              candidates: projects
                .filter((candidate) => !candidate.archived)
                .map((candidate) => ({
                  id: candidate.id,
                  name: candidate.name,
                  thumbnail: thumbnails[candidate.id] || undefined,
                  open: openWorkspaces.some(
                    (item) => item.project.id === candidate.id,
                  ),
                })),
              onSelect: activateWorkspace,
              onClose: (projectId) => void closeWorkspace(projectId),
              onOpenProject: (projectId) => {
                const candidate = projects.find((item) => item.id === projectId)
                if (candidate) openWorkspace({ project: candidate })
              },
            }}
            onBack={() => {
              setActiveWorkspaceId(null)
              void refresh()
            }}
            onDownload={() => projectService.exportProject(target.project)}
            onEdit={() => {
              releaseWorkspace(target.project.id)
              openModal("rename", target.project)
            }}
            onOpenSettings={onOpenSettings}
            onOpenCommandMenu={onOpenCommandMenu}
            onDelete={() => {
              releaseWorkspace(target.project.id)
              openModal("delete", target.project)
            }}
            onOpenFolder={() => projectService.openProject(target.project.path)}
          />
        </div>
      ))}

    </section>
  )
}

function ProjectThumbnail({
  source,
  loading,
}: {
  source: string
  loading: boolean
}) {
  const [failed, setFailed] = useState(false)
  const usableSource = source.startsWith("data:image/")

  useEffect(() => {
    setFailed(false)
  }, [source])

  return (
    <span className="relative flex h-12 w-[4.5rem] shrink-0 items-center justify-center overflow-hidden rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)] sm:h-14 sm:w-20">
      {usableSource && !failed ? (
        <img
          src={source}
          alt=""
          loading="lazy"
          decoding="async"
          draggable={false}
          onError={() => setFailed(true)}
          className="size-full object-cover"
        />
      ) : (
        <span
          data-project-thumbnail="workspace"
          aria-hidden="true"
          className="relative size-full overflow-hidden"
          style={{
            background:
              "linear-gradient(145deg, var(--surface-container-high), var(--primary-container))",
          }}
        >
          <span
            className="absolute inset-0 opacity-70"
            style={{
              backgroundImage:
                "radial-gradient(circle, var(--dot) 1px, transparent 1px)",
              backgroundSize: "8px 8px",
            }}
          />
          <span className="absolute left-2 top-2 h-8 w-11 overflow-hidden rounded-md border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_2px_7px_var(--shadow-soft)]">
            <span className="flex h-2 items-center gap-0.5 border-b border-[var(--outline-variant)] px-1">
              <span className="size-1 rounded-full bg-[var(--primary)]" />
              <span className="size-1 rounded-full bg-[var(--outline-variant)]" />
            </span>
            <span className="mx-1.5 mt-1.5 block h-1 w-6 rounded-full bg-[var(--primary-container)]" />
            <span className="mx-1.5 mt-1 block h-1 w-4 rounded-full bg-[var(--outline-variant)]" />
          </span>
          <span className="absolute bottom-1.5 right-1.5 flex h-6 w-9 items-center rounded-md border border-[var(--outline-variant)] bg-[var(--tooltip)] px-1.5 font-mono text-[0.48rem] font-semibold text-[var(--tooltip-foreground)] shadow-[0_3px_9px_var(--shadow-soft)]">
            &gt;_
          </span>
        </span>
      )}
      {loading && (
        <span className="absolute inset-0 flex items-center justify-center bg-black/20 text-white backdrop-blur-[1px]">
          <LoaderCircle className="size-4 animate-spin" />
        </span>
      )}
    </span>
  )
}

function ProjectRow({
  project,
  thumbnail,
  busy,
  nested = false,
  onOpen,
  onFavorite,
  onRename,
  onGitHub,
  onBackup,
  onArchive,
  onDelete,
}: {
  project: Project
  thumbnail: string
  busy: string | null
  nested?: boolean
  onOpen: () => void
  onFavorite: () => void
  onRename: () => void
  onGitHub: () => void
  onBackup: () => void
  onArchive: () => void
  onDelete: () => void
}) {
  const hasGitHub = isGitHubRemote(project.gitRemote)

  return (
    <article
      className={cn(
        "group relative flex min-w-0 items-center gap-3 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-3 py-3 shadow-[0_1px_3px_var(--shadow-soft)] transition-colors hover:bg-[var(--state-layer)] sm:px-4",
        nested &&
          "rounded-none border-0 bg-transparent pl-5 shadow-none last:rounded-b-2xl sm:pl-7",
      )}
    >
      <button
        type="button"
        aria-label={`Open workspace for ${project.name}`}
        disabled={busy !== null}
        onClick={onOpen}
        className="shrink-0 rounded-xl outline-none transition-transform active:scale-[0.98] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-60"
      >
        <ProjectThumbnail
          source={thumbnail}
          loading={busy === `open-${project.id}`}
        />
      </button>

      <button
        type="button"
        disabled={busy !== null}
        onClick={onOpen}
        className="min-w-0 flex-1 text-left outline-none disabled:opacity-60"
      >
        <span className="flex min-w-0 items-center gap-2">
          <span className="truncate text-sm font-semibold tracking-[-0.015em]">
            {project.name}
          </span>
          {project.variantLabel && (
            <span className="hidden shrink-0 rounded-md bg-[var(--surface-container)] px-1.5 py-0.5 text-[0.62rem] font-medium text-[var(--on-surface-variant)] sm:inline">
              {project.variantLabel}
            </span>
          )}
        </span>
        <span className="mt-1 flex min-w-0 items-center gap-2 text-[0.68rem] text-[var(--on-surface-variant)]">
          <span className="shrink-0">{sourceLabels[project.source]}</span>
          {project.branch && (
            <>
              <span aria-hidden="true">·</span>
              <span className="shrink-0">{project.branch}</span>
            </>
          )}
          {hasGitHub && (
            <>
              <span aria-hidden="true">·</span>
              <GitFork className="size-3 shrink-0" strokeWidth={1.7} />
            </>
          )}
          <span aria-hidden="true">·</span>
          <span className="truncate" title={project.path}>
            {project.path}
          </span>
        </span>
      </button>

      <div className="flex shrink-0 items-center gap-0.5">
        <IconAction
          label={
            project.favorite ? "Remove from favorites" : "Mark as favorite"
          }
          active={project.favorite}
          disabled={busy !== null}
          onClick={onFavorite}
        >
          <Star
            className="size-4"
            fill={project.favorite ? "currentColor" : "none"}
            strokeWidth={1.7}
          />
        </IconAction>
        <ProjectActions
          project={project}
          busy={busy}
          onRename={onRename}
          onGitHub={onGitHub}
          onBackup={onBackup}
          onArchive={onArchive}
          onDelete={onDelete}
        />
      </div>
    </article>
  )
}

function ProjectActions({
  project,
  busy,
  onRename,
  onGitHub,
  onBackup,
  onArchive,
  onDelete,
}: {
  project: Project
  busy: string | null
  onRename: () => void
  onGitHub: () => void
  onBackup: () => void
  onArchive: () => void
  onDelete: () => void
}) {
  const hasGitHub = isGitHubRemote(project.gitRemote)
  const detailsRef = useRef<HTMLDetailsElement>(null)
  const summaryRef = useRef<HTMLElement>(null)
  const close = () => detailsRef.current?.removeAttribute("open")
  const act = (action: () => void) => () => {
    close()
    action()
  }

  return (
    <details
      ref={detailsRef}
      className="relative"
      onBlur={(event) => {
        if (!event.currentTarget.contains(event.relatedTarget)) close()
      }}
      onKeyDown={(event) => {
        if (event.key === "Escape") {
          event.preventDefault()
          close()
          summaryRef.current?.focus()
        }
      }}
    >
      <summary
        ref={summaryRef}
        aria-label={`More actions for ${project.name}`}
        aria-disabled={busy !== null}
        title="More actions"
        tabIndex={busy !== null ? -1 : 0}
        onClick={(event) => {
          if (busy !== null) event.preventDefault()
        }}
        className="flex size-8 list-none items-center justify-center rounded-lg text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--surface-container)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&::-webkit-details-marker]:hidden"
      >
        <MoreHorizontal className="size-4" strokeWidth={1.8} />
      </summary>

      <div
        aria-label={`Actions for ${project.name}`}
        className="dropdown-menu-in absolute right-0 top-full z-30 mt-1.5 w-48 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-1.5 shadow-[0_12px_32px_var(--shadow-elevated)] backdrop-blur-xl"
      >
        <MenuAction onClick={act(onGitHub)}>
          <GitFork className="size-3.5" />
          {hasGitHub ? "Change GitHub" : "Link GitHub"}
        </MenuAction>
        <MenuAction
          disabled={busy !== null || !hasGitHub}
          title={
            hasGitHub
              ? "Create backup"
              : "Link a repository before backing up"
          }
          onClick={act(onBackup)}
        >
          <CloudUpload className="size-3.5" />
          Create backup
        </MenuAction>
        <MenuAction onClick={act(onRename)}>
          <Pencil className="size-3.5" />
          Rename
        </MenuAction>
        <MenuAction onClick={act(onArchive)}>
          {project.archived ? (
            <RotateCcw className="size-3.5" />
          ) : (
            <Archive className="size-3.5" />
          )}
          {project.archived ? "Restore" : "Archive"}
        </MenuAction>
        <MenuAction
          destructive
          onClick={act(onDelete)}
        >
          <Trash2 className="size-3.5" />
          {project.source === "imported" ? "Remove from Seizen" : "Delete"}
        </MenuAction>
      </div>
    </details>
  )
}

function MenuAction({
  destructive = false,
  className,
  ...props
}: ComponentProps<"button"> & { destructive?: boolean }) {
  return (
    <button
      type="button"
      className={cn(
        "flex h-8 w-full items-center gap-2 rounded-lg px-2.5 text-left text-xs text-[var(--on-surface-variant)] outline-none hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:bg-[var(--state-layer)] disabled:opacity-40",
        destructive && "text-[var(--error)] hover:text-[var(--error)]",
        className,
      )}
      {...props}
    />
  )
}

function ProjectIdentity({
  project,
  destructive = false,
}: {
  project: Project
  destructive?: boolean
}) {
  return (
    <div className="flex min-w-0 items-center gap-3 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] p-3">
      <span
        className={cn(
          "flex size-9 shrink-0 items-center justify-center rounded-xl bg-[var(--primary-container)] text-[var(--on-primary-container)]",
          destructive && "bg-[var(--state-layer)] text-[var(--error)]",
        )}
      >
        {destructive ? (
          <Trash2 className="size-4" strokeWidth={1.7} />
        ) : (
          <GitFork className="size-4" strokeWidth={1.7} />
        )}
      </span>
      <div className="min-w-0">
        <p className="truncate text-sm font-semibold">{project.name}</p>
        <p
          className="mt-0.5 truncate text-[0.68rem] text-[var(--on-surface-variant)]"
          title={project.path}
        >
          {project.path}
        </p>
      </div>
    </div>
  )
}

function IconAction({
  label,
  active,
  className,
  ...props
}: ComponentProps<"button"> & { label: string; active?: boolean }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      className={cn(
        "flex size-8 items-center justify-center rounded-lg text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--surface-container)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] disabled:opacity-40",
        active && "text-[var(--primary)]",
        className,
      )}
      {...props}
    />
  )
}

export { ProjectLibrary }
