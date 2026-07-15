import {
  useCallback,
  useEffect,
  useMemo,
  useState,
  type FormEvent,
  type ReactNode,
} from "react"
import {
  Activity,
  Bot,
  Camera,
  CheckCircle2,
  ChevronDown,
  Circle,
  CircleAlert,
  ExternalLink,
  FlaskConical,
  LoaderCircle,
  Monitor,
  Pencil,
  Play,
  Plus,
  RotateCw,
  Save,
  Smartphone,
  Square,
  Tablet,
  TerminalSquare,
  Trash2,
  X,
} from "lucide-react"
import { BrowserOpenURL, EventsOn } from "../../../wailsjs/runtime/runtime"

import { BrandChip } from "@/components/ui/brand-icon"
import { Button } from "@/components/ui/button"
import { confirmDialog, confirmWithOption } from "@/components/ui/confirm"
import { Input } from "@/components/ui/input"
import { Select } from "@/components/ui/select"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"
import { cn } from "@/lib/utils"

import {
  projectService,
  type AgentApproval,
  type AgentSession,
  type AppCandidate,
  type AppInput,
  type AppKind,
  type AppRuntimeStatus,
  type AppStatus,
  type BrowserAutomationResult,
  type BrowserAutomationStatus,
  type Experiment,
  type ExperimentReview,
  type Project,
  type ProjectApp,
  type ProjectContext,
  type ProjectTerminalSession,
} from "./project-service"
import { ExperimentComparison, ExperimentSelector } from "./ExperimentSelector"
import { ExperimentCreateDialog, type ExperimentDraft } from "./ExperimentCreateDialog"
import {
  normalizeAppPreviewURL,
  normalizeBrowserURL,
  type TerminalShell,
} from "./workspace-model"

type Viewport = "desktop" | "tablet" | "mobile"

const statusLabels: Record<AppStatus, string> = {
  unconfigured: "Unconfigured",
  stopped: "Stopped",
  starting: "Starting",
  running: "Running",
  testing: "Testing",
  failed: "Failed",
  stopping: "Stopping",
}

function appStatusDot(status: AppStatus) {
  return status === "running"
    ? "bg-[var(--success)]"
    : status === "starting" || status === "testing" || status === "stopping"
      ? "bg-[var(--warning)] animate-pulse"
      : status === "failed"
        ? "bg-[var(--error)]"
        : "bg-[var(--outline-variant)]"
}

const viewportWidths: Record<Viewport, string> = {
  desktop: "100%",
  tablet: "768px",
  mobile: "390px",
}

export type AppTerminalSession = {
  nodeId: string
  sessionId: string
  agent: TerminalShell
  name: string
  status: "starting" | "running" | "exited" | "error"
}

type AppFlow =
  | { stage: "idle" }
  | { stage: "choose-agent"; sessions: AgentSession[] }
  | { stage: "confirm"; session: AgentSession; task: string }
  | { stage: "working"; session: AgentSession }
  | { stage: "choose-terminal"; sessions: ProjectTerminalSession[] }
  | { stage: "waiting-terminal"; session: ProjectTerminalSession; detection?: ManagedDetection }
  | { stage: "failed"; session?: AgentSession; details: MountFailure }

type ManagedDetection = {
  sessionId: string
  projectId: string
  spaceId: string
  candidates: Array<{ pid: number; port: number; url: string; managed: boolean }>
  diagnostic: string
}

type MountFailure = {
  step: string
  error: string
  exitCode?: number
  logs?: string
  expectedPort?: number
}

const mountTask = `Analyze this project and mount its main application in Seizen.

1. Get the context via seizen_project_context.
2. Detect candidate Apps via seizen_app_discover.
3. If there is a single clear candidate, configure it.
4. If there are several relevant Apps, ask which one to mount or record the candidates without silently choosing one.
5. Install dependencies if needed.
6. Do not run the main process outside Seizen.
7. Configure the App using the MCP tools.
8. Start it via seizen_app_mount or seizen_app_run.
9. Wait until the process, port, or executable is actually available.
10. Register the preview when applicable.
11. Run a smoke test.
12. Check console errors and logs.
13. Clearly report the result.`

const progressSteps = [
  ["Analyzing the project", "agent.task.sent", "app.discovery.started"],
  ["Detecting the application", "app.discovery.started", "app.discovery.completed"],
  ["Configuring", "app.configuration.started", "app.configuration.completed"],
  ["Starting", "app.mount.started", "app.process.started"],
  ["Waiting for preview", "app.process.started", "app.preview.ready"],
  ["Testing", "app.test.started", "app.test.completed"],
] as const

function mountEventLabel(event: string) {
  for (const [label, started, completed] of progressSteps) {
    if (event === started || event === completed) return label
  }
  return event
}

function toolLabel(tool: string) {
  return tool.replace(/^seizen[._]/, "").replaceAll("_", " ")
}

const sessionStatusLabels: Record<string, string> = {
  active: "Active",
  running: "Running",
  stopped: "Stopped",
  closed: "Closed",
  failed: "Failed",
}

function sessionStatusLabel(status: string) {
  return sessionStatusLabels[status] ?? status
}

export function AppView({
  project,
  context,
  onSelectExperiment,
  onSelectedAppId,
  terminalSessions,
  onOpenTerminal,
  onFocusTerminal,
  onPreviewReady,
}: {
  project: Project
  context: ProjectContext
  onSelectExperiment: (experimentId: string) => Promise<void>
  onSelectedAppId?: (appId: string) => void
  terminalSessions: AppTerminalSession[]
  onOpenTerminal: (shell: TerminalShell) => Promise<AppTerminalSession>
  onFocusTerminal: (sessionId: string) => void
  onPreviewReady?: () => void
}) {
  const [apps, setApps] = useState<ProjectApp[]>([])
  const [experiments, setExperiments] = useState<Experiment[]>([])
  const [creatingExperiment, setCreatingExperiment] = useState(false)
  const [approvals, setApprovals] = useState<AgentApproval[]>([])
  const [integrationApprovals, setIntegrationApprovals] = useState<AgentApproval[]>([])
  const [reviewApproval, setReviewApproval] = useState("")
  const [experimentReview, setExperimentReview] = useState<ExperimentReview | null>(null)
  const [comparisonText, setComparisonText] = useState("")
  const [selectedId, setSelectedId] = useState("")
  const [editor, setEditor] = useState<"new" | "edit" | "candidates" | null>(null)
  const [manualSuggestion, setManualSuggestion] = useState<AppCandidate>()
  const [runtime, setRuntime] = useState<AppRuntimeStatus | null>(null)
  const [logs, setLogs] = useState("")
  const [automation, setAutomation] = useState<BrowserAutomationStatus | null>(null)
  const [automationResult, setAutomationResult] = useState<BrowserAutomationResult | null>(null)

  useEffect(() => {
    onSelectedAppId?.(selectedId)
  }, [onSelectedAppId, selectedId])
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [automationBusy, setAutomationBusy] = useState(false)
  const [error, setError] = useState("")
  const [operationMessage, setOperationMessage] = useState("")
  const [viewport, setViewport] = useState<Viewport>("desktop")
  const [flow, setFlow] = useState<AppFlow>({ stage: "idle" })
  const [candidates, setCandidates] = useState<AppCandidate[]>([])
  const [completedEvents, setCompletedEvents] = useState<Set<string>>(new Set())
  const [lastEvent, setLastEvent] = useState("")
  const [lastTool, setLastTool] = useState("")

  const load = useCallback(async () => {
    try {
      const [next, nextExperiments, nextApprovals] = await Promise.all([
        projectService.listApps(project.id),
        projectService.listExperiments(project.id, "app"),
        projectService.listPendingAgentApprovals(project.id),
      ])
      setExperiments(nextExperiments ?? [])
      setApps(next)
      setApprovals((nextApprovals ?? []).filter((approval) =>
        approval.action === "experiment.create" && (approval.experimentId ?? "") === context.experimentId,
      ))
      setIntegrationApprovals((nextApprovals ?? []).filter((approval) =>
        approval.action === "experiment.integrate" && approval.experimentId === context.experimentId,
      ))
      setSelectedId((current) =>
        next.some((app) => app.id === current) ? current : (next[0]?.id ?? ""),
      )
      setError("")
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setLoading(false)
    }
  }, [context.experimentId, project.id])

  const refreshRuntime = useCallback(async (appId: string) => {
    if (!appId) return
    const [statusResult, logsResult] = await Promise.allSettled([
      projectService.getAppStatus(appId, context.experimentId),
      projectService.getAppLogs(appId, context.experimentId),
    ])
    if (statusResult.status === "fulfilled") {
      setRuntime(statusResult.value)
      setApps((current) =>
        current.map((item) =>
          item.id === appId ? statusResult.value.app : item,
        ),
      )
    } else {
      setError(errorMessage(statusResult.reason))
    }
    if (logsResult.status === "fulfilled") setLogs(logsResult.value)
  }, [context.experimentId])

  useEffect(() => {
    setLoading(true)
    setEditor(null)
    setManualSuggestion(undefined)
    setCandidates([])
    setRuntime(null)
    setLogs("")
    setAutomation(null)
    setAutomationResult(null)
    void load()
  }, [load])

  // App discovery is on-demand ("Add App"): an automatic sweep
  // in large projects detects too much and is expensive.

  const storedApp = apps.find((candidate) => candidate.id === selectedId)
  const app = runtime?.app.id === selectedId ? runtime.app : storedApp
  const needsConfiguration = app?.status === "unconfigured"

  useEffect(() => {
    setRuntime(null)
    setLogs("")
    setAutomation(null)
    setAutomationResult(null)
    setOperationMessage("")
    if (!selectedId) return
    void refreshRuntime(selectedId)
    if (storedApp?.kind === "web") {
      if (!storedApp.previewUrl) {
        setAutomation({
          provider: "",
          available: false,
          browserFeatures: false,
          message: "Set a preview URL to use web automation.",
        })
      } else {
        void projectService
          .getBrowserAutomationStatus(selectedId)
          .then(setAutomation)
          .catch((caught: unknown) => setAutomation({
            provider: "",
            available: false,
            browserFeatures: false,
            message: errorMessage(caught),
          }))
      }
    }
  }, [refreshRuntime, selectedId, storedApp?.kind, storedApp?.previewUrl])

  const appId = app?.id
  const appActive = Boolean(
    app && ["starting", "running", "testing", "stopping"].includes(app.status),
  )

  useEffect(() => {
    if (!appId || !appActive) return
    const timer = window.setInterval(() => void refreshRuntime(appId), 1_200)
    return () => window.clearInterval(timer)
  }, [appActive, appId, refreshRuntime])

  useEffect(() => {
    // Debounce: bursts of events arrive during provisioning; a single reload is enough.
    let timer: number | undefined
    const refresh = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => {
        void load()
        if (selectedId) void refreshRuntime(selectedId)
      }, 150)
    }
    const unsubscribe = [
      "app.created",
      "app.updated",
      "app.starting",
      "app.running",
      "app.testing",
      "app.failed",
      "app.stopping",
      "app.stopped",
      "app.preview.updated",
      "app.primary.updated",
      "experiment.created",
      "experiment.status.updated",
      "experiment.selected",
      "agent.approval.requested",
      "agent.approval.resolved",
    ].map((event) => EventsOn(event, refresh))
    return () => {
      window.clearTimeout(timer)
      unsubscribe.forEach((off) => off())
    }
  }, [load, refreshRuntime, selectedId])

  useEffect(() => {
    const progressEvents = [
      "app.discovery.started",
      "app.discovery.completed",
      "app.configuration.started",
      "app.configuration.completed",
      "app.mount.started",
      "app.process.started",
      "app.port.detected",
      "app.preview.ready",
      "app.test.started",
      "app.test.completed",
    ]
    const offProgress = progressEvents.map((name) => EventsOn(name, (payload: unknown) => {
      if (!belongsToProject(payload, project.id)) return
      setLastEvent(name)
      setCompletedEvents((current) => new Set(current).add(name))
      if (name === "app.preview.ready") onPreviewReady?.()
    }))
    const offAudit = EventsOn("agent.audit", (payload: unknown) => {
      if (!belongsToProject(payload, project.id) || !isRecord(payload)) return
      if (typeof payload.toolName === "string") setLastTool(payload.toolName)
    })
    const offFailed = EventsOn("app.mount.failed", (payload: unknown) => {
      if (!belongsToProject(payload, project.id)) return
      const record = isRecord(payload) ? payload : {}
      setFlow((current) => ({
        stage: "failed",
        session: "session" in current ? current.session as AgentSession : undefined,
        details: {
          step: stringValue(record.step, "Mount"),
          error: stringValue(record.error, "The mount could not be completed"),
          exitCode: numberValue(record.exitCode),
          logs: stringValue(record.logs),
          expectedPort: numberValue(record.expectedPort),
        },
      }))
    })
    return () => {
      offProgress.forEach((off) => off())
      offAudit()
      offFailed()
    }
  }, [onPreviewReady, project.id])

  const run = async (
    action: () => Promise<unknown>,
    successMessage = "",
  ) => {
    setBusy(true)
    setError("")
    setOperationMessage("")
    try {
      await action()
      if (successMessage) setOperationMessage(successMessage)
      await load()
      if (selectedId) await refreshRuntime(selectedId)
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const runAutomation = async (
    action: () => Promise<BrowserAutomationResult>,
  ) => {
    setAutomationBusy(true)
    setAutomationResult(null)
    setError("")
    try {
      setAutomationResult(await action())
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setAutomationBusy(false)
    }
  }

  const saveApp = async (input: AppInput) => {
    setBusy(true)
    setError("")
    try {
      const saved = app && (editor === "edit" || needsConfiguration)
        ? await projectService.updateApp(app.id, input)
        : await projectService.createApp(input)
      setSelectedId(saved.id)
      setEditor(null)
      setManualSuggestion(undefined)
      await load()
    } finally {
      setBusy(false)
    }
  }

  const askAI = async () => {
    setBusy(true)
    setError("")
    try {
      const sessions = (await projectService.listProjectAgentSessions(
        project.id,
        "workspace",
        context.experimentId,
      ))
        .filter((session) => session.status === "active" || session.status === "running")
      if (sessions.length === 1) {
        setFlow({ stage: "confirm", session: sessions[0], task: mountTask })
      } else if (sessions.length > 1) {
        setFlow({ stage: "choose-agent", sessions })
      } else {
        setFlow({ stage: "choose-agent", sessions: [] })
      }
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  // Reuses a live session of that agent or opens a new one.
  const askAgent = async (shell: "claude" | "codex") => {
    setBusy(true)
    setError("")
    try {
      const sessions = (await projectService.listProjectAgentSessions(
        project.id,
        "workspace",
        context.experimentId,
      )).filter((session) =>
        (session.status === "active" || session.status === "running") &&
        session.agent === shell,
      )
      if (sessions.length > 0) {
        setFlow({ stage: "confirm", session: sessions[0], task: mountTask })
        setBusy(false)
        return
      }
    } catch {
      // If sessions couldn't be listed, a new one is opened anyway.
    }
    setBusy(false)
    await createAgent(shell)
  }

  const createAgent = async (shell: "claude" | "codex") => {
    setBusy(true)
    setError("")
    try {
      const opened = await onOpenTerminal(shell)
      const session: AgentSession = {
        id: opened.sessionId,
        projectId: project.id,
        experimentId: context.experimentId,
        spaceId: "workspace",
        appId: "",
        agent: shell,
        name: opened.name,
        status: "running",
        terminalSessionId: opened.sessionId,
      }
      setFlow({ stage: "confirm", session, task: mountTask })
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const sendTask = async (session: AgentSession, task: string) => {
    setBusy(true)
    setError("")
    try {
      await projectService.sendAgentTask(
        project.id,
        session.id,
        task,
        "workspace",
        context.experimentId,
      )
      setCompletedEvents(new Set(["agent.task.sent"]))
      setLastEvent("agent.task.sent")
      setLastTool("")
      setFlow({ stage: "working", session })
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const cancelAgentWork = async (session: AgentSession) => {
    setBusy(true)
    setError("")
    try {
      await projectService.cancelAgentTask(
        project.id,
        session.terminalSessionId,
        session.spaceId || "workspace",
        context.experimentId,
      )
      if (app && ["starting", "running", "testing"].includes(app.status)) {
        await projectService.stopApp(app.id, context.experimentId)
      }
      setFlow({ stage: "idle" })
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const chooseTerminal = async () => {
    setBusy(true)
    setError("")
    try {
      setFlow({
        stage: "choose-terminal",
        sessions: (await projectService.listProjectTerminalSessions(
          project.id,
          "workspace",
          context.experimentId,
        ))
          .filter((session) => session.status === "active" || session.status === "running"),
      })
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const openManualTerminal = async (shell: TerminalShell) => {
    setBusy(true)
    setError("")
    try {
      const opened = await onOpenTerminal(shell)
      setFlow({
        stage: "waiting-terminal",
        session: {
          sessionId: opened.sessionId,
          projectId: project.id,
          spaceId: "workspace",
          shell,
          status: "running",
          name: opened.name,
        },
      })
      onFocusTerminal(opened.sessionId)
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const detectTerminal = async (session: ProjectTerminalSession) => {
    setBusy(true)
    setError("")
    try {
      const detection = await projectService.detectManagedApp(project.id, session.sessionId)
      setFlow({ stage: "waiting-terminal", session, detection })
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const attachTerminal = async (
    session: ProjectTerminalSession,
    previewValue: string,
    detection?: ManagedDetection,
  ) => {
    setBusy(true)
    setError("")
    try {
      const previewUrl = normalizeAppPreviewURL(previewValue)
      if (!previewUrl) throw new Error("Enter a preview URL")
      let attachedApp = app ?? apps[0]
      if (!attachedApp && candidates.length === 1 && canMountCandidate(candidates[0])) {
        attachedApp = await projectService.createApp(candidateInput(project.id, candidates[0], previewUrl))
      }
      const attached = await projectService.attachRunningApp({
        projectId: project.id,
        spaceId: "workspace",
        appId: attachedApp?.id ?? "",
        terminalSessionId: session.sessionId,
        previewUrl,
        detectedPort: detection?.candidates[0]?.port ?? Number(new URL(previewUrl).port || 0),
        discoverySource: detection ? "detected" : "manual",
        confirmed: true,
        name: attachedApp?.name ?? project.name,
        kind: attachedApp?.kind ?? "web",
        workingDirectory: attachedApp?.workingDirectory ?? project.path,
      })
      setSelectedId(attached.app.id)
      setFlow({ stage: "idle" })
      await load()
      await refreshRuntime(attached.app.id)
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const mountCandidate = async (candidate: AppCandidate) => {
    await run(async () => {
      const mounted = await projectService.createApp(candidateInput(project.id, candidate))
      setSelectedId(mounted.id)
      setFlow({ stage: "idle" })
      setEditor(null)
      setManualSuggestion(undefined)
      await projectService.startApp(mounted.id, context.experimentId)
    })
  }

  const openManual = (candidate?: AppCandidate) => {
    setManualSuggestion(candidate)
    setEditor("new")
  }

  const createExperiment = async (draft: ExperimentDraft) => {
    setBusy(true)
    setError("")
    try {
      const experiment = await projectService.createExperiment({
        projectId: project.id,
        kind: "app",
        appId: draft.appId,
        baseServerId: "",
        name: draft.name,
        objective: draft.objective,
        branchName: "",
        createdBy: "user",
        agentSessionId: "",
        riskLevel: "low",
        riskReasonsJson: "[]",
        configurationJson: "",
        confirmed: true,
      })
      await onSelectExperiment(experiment.id)
      const sessionId = await projectService.startProjectAgentTerminal(
        project,
        draft.agent,
        draft.appId,
        experiment.id,
      )
      await projectService.linkExperimentAgentSession(experiment.id, sessionId)
      setCreatingExperiment(false)
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const prepareExperiment = async () => {
    if (!context.experimentId) return
    setBusy(true)
    setError("")
    try {
      setExperimentReview(await projectService.prepareExperimentIntegration(context.experimentId))
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
      await load()
    } finally {
      setBusy(false)
    }
  }

  const compareExperiment = async () => {
    if (!context.experimentId) return
    setBusy(true)
    try {
      const comparison = await projectService.compareExperiment(context.experimentId)
      setComparisonText(`${comparison.stat || "No changes"}\n\n${comparison.files}`)
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const deleteApp = async () => {
    if (!app) return
    const accepted = await confirmDialog({
      title: "Delete App",
      message: `This will delete the configuration for ${app.name}. The project code is not touched.`,
      confirmLabel: "Delete",
      tone: "danger",
    })
    if (!accepted) return
    await run(async () => {
      await projectService.deleteApp(app.id)
      setSelectedId("")
      setEditor(null)
    }, "App deleted")
  }

  const discardExperiment = async () => {
    if (!context.experimentId) return
    const { accepted, option: deleteBranch } = await confirmWithOption({
      title: "Discard experiment",
      message: "This experiment will be discarded and all its resources will be stopped.",
      confirmLabel: "Discard",
      tone: "danger",
      optionLabel: "Also delete the experiment's git branch",
    })
    if (!accepted) return
    setBusy(true)
    setError("")
    try {
      await projectService.discardExperiment(context.experimentId, false, deleteBranch)
      await onSelectExperiment("")
      await load()
    } catch (caught) {
      const message = errorMessage(caught)
      if (message.includes("uncommitted changes") && (await confirmDialog({
        title: "Create checkpoint",
        message: `${message}. Create a checkpoint before discarding it?`,
        confirmLabel: "Create checkpoint",
      }))) {
        await projectService.discardExperiment(context.experimentId, true, deleteBranch)
        await onSelectExperiment("")
        await load()
      } else {
        setError(message)
      }
    } finally {
      setBusy(false)
    }
  }

  if (loading) return <ViewLoading label="Loading Apps" />

  return (
    <section
      role="tabpanel"
      aria-label={`App for ${project.name}`}
      className="view-enter absolute inset-0 overflow-auto bg-[var(--surface)] px-5 pb-6 pt-20"
    >
      <div className="mx-auto flex min-h-full max-w-[1180px] flex-col">
        {apps.length > 0 && (
          <ExperimentSelector
            principalLabel={`${apps.find((item) => item.isPrimary)?.name ?? apps[0].name} Main`}
            context={context}
            experiments={experiments}
            onSelect={(experimentId) => {
              setError("")
              void onSelectExperiment(experimentId).catch((caught: unknown) => setError(errorMessage(caught)))
            }}
            onNew={() => setCreatingExperiment(true)}
            onRestore={(experimentId) => run(async () => {
              const accepted = await confirmDialog({
                title: "Restore experiment",
                message: "This will restore the branch and worktree for this experiment.",
                confirmLabel: "Restore",
              })
              if (!accepted) return
              await projectService.restoreExperiment(experimentId)
              await onSelectExperiment(experimentId)
            })}
          />
        )}
        {creatingExperiment && (
          <ExperimentCreateDialog
            kind="app"
            apps={apps}
            busy={busy}
            onCancel={() => setCreatingExperiment(false)}
            onCreate={createExperiment}
          />
        )}
        {approvals[0] && (
          <div className="mb-4 rounded-xl border border-[var(--focus-border)] bg-[var(--primary-container)] p-4 text-xs text-[var(--on-primary-container)]">
            <p className="font-semibold">This change could affect an important part of the project</p>
            <p className="mt-1 opacity-80">Do you want to create a new experiment to keep the main version intact?</p>
            {reviewApproval === approvals[0].id && (
              <pre className="mt-3 max-h-40 overflow-auto whitespace-pre-wrap rounded-lg bg-black/10 p-3 text-[0.68rem]">{approvals[0].requestJson}</pre>
            )}
            <div className="mt-3 flex flex-wrap gap-2">
              <Button type="button" disabled={busy} onClick={() => run(() => projectService.resolveAgentApproval(approvals[0].id, true))}>Create experiment</Button>
              <Button type="button" variant="ghost" disabled={busy} onClick={() => run(async () => {
                const approval = approvals[0]
                const confirmedCritical = approval.requestJson.includes('"riskLevel":"critical"')
                  ? await confirmDialog({
                      title: "Critical change",
                      message: "This critical change may be irreversible. Do you confirm continuing on Main?",
                      confirmLabel: "Continue",
                      tone: "danger",
                    })
                  : false
                await projectService.continueExperimentChangeOnPrincipal(approval.id, confirmedCritical)
              })}>Continue on Main</Button>
              <Button type="button" variant="ghost" onClick={() => setReviewApproval((current) => current === approvals[0].id ? "" : approvals[0].id)}>Review plan</Button>
            </div>
          </div>
        )}
        {context.experimentId && (
          <div className="mb-4 flex flex-wrap items-center gap-2 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] px-3 py-2 text-xs">
            <span className="mr-auto font-medium">{context.name} · {context.branchName}</span>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void compareExperiment()}>Compare</Button>
            <Button type="button" disabled={busy} onClick={() => void prepareExperiment()}>Integrate into Main</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => run(async () => {
              await projectService.archiveExperiment(context.experimentId)
              await onSelectExperiment("")
            })}>Keep experiment</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void discardExperiment()} className="text-[var(--error)]">Discard</Button>
          </div>
        )}
        {comparisonText && (
          <ExperimentComparison text={comparisonText} onClose={() => setComparisonText("")} />
        )}
        {experimentReview && (
          <div className="mb-4 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 text-xs">
            <p className="font-semibold">Review ready · {experimentReview.comparison.commitCount} commit(s)</p>
            <p className="mt-1 text-[var(--on-surface-variant)]">Tests: {experimentReview.testsPassed ? "passed" : "failed"} · App: {experimentReview.appVerified ? "verified" : "unverified"}</p>
            {experimentReview.conflicts.length > 0 && <p className="mt-2 text-[var(--error)]">Conflicts: {experimentReview.conflicts.join(", ")}</p>}
            {experimentReview.secretFindings.length > 0 && <p className="mt-2 text-[var(--error)]">Possible secrets detected.</p>}
            {experimentReview.testsPassed && experimentReview.appVerified && experimentReview.conflicts.length === 0 && (
              <Button type="button" className="mt-3" disabled={busy} onClick={() => run(() => projectService.requestExperimentIntegration(context.experimentId))}>Request final confirmation</Button>
            )}
          </div>
        )}
        {integrationApprovals[0] && (
          <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-[var(--focus-border)] bg-[var(--primary-container)] px-4 py-3 text-xs text-[var(--on-primary-container)]">
            <span className="mr-auto font-semibold">The temporary integration passed review. Main has not been modified yet.</span>
            <Button type="button" disabled={busy} onClick={() => run(async () => {
              await projectService.resolveAgentApproval(integrationApprovals[0].id, true)
              await projectService.integrateExperiment(context.experimentId, integrationApprovals[0].id)
            })}>Integrate into Main</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => run(() => projectService.resolveAgentApproval(integrationApprovals[0].id, false))}>Cancel</Button>
          </div>
        )}
        <div className="mb-4 flex min-h-9 items-center gap-2">
          {apps.length > 0 && (
            <Select
              value={selectedId}
              onChange={(event) => {
                setSelectedId(event.target.value)
                setEditor(null)
                setManualSuggestion(undefined)
              }}
              aria-label="Selected App"
              wrapperClassName="w-auto"
              className="min-w-44 rounded-full bg-[var(--surface-container-high)] text-xs"
            >
              {apps.map((item) => (
                <option key={item.id} value={item.id}>{item.name}{item.isPrimary ? " · Main" : ""}</option>
              ))}
            </Select>
          )}
          {apps.length > 0 && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setManualSuggestion(undefined)
                void (async () => {
                  setBusy(true)
                  try {
                    const items = await projectService.discoverApps(project.id)
                    setCandidates(items ?? [])
                    setEditor(items?.length ? "candidates" : "new")
                  } catch {
                    setEditor("new")
                  } finally {
                    setBusy(false)
                  }
                })()
              }}
              disabled={busy}
              className="h-9 rounded-full px-3 text-xs text-[var(--on-surface-variant)]"
            >
              <Plus className="size-3.5" /> Add App
            </Button>
          )}
          {app && (
            <>
              {!app.isPrimary && <Button type="button" variant="ghost" onClick={() => run(() => projectService.setPrimaryApp(project.id, app.id))} disabled={busy} className="ml-auto h-8 rounded-full text-xs">Mark as Main</Button>}
              <span className={cn("flex items-center gap-1.5 rounded-full bg-[var(--surface-container)] px-3 py-1.5 text-[0.68rem] text-[var(--on-surface-variant)]", app.isPrimary && "ml-auto")}>
                <span
                  aria-hidden="true"
                  className={cn("size-1.5 rounded-full", appStatusDot(app.status))}
                />
                {statusLabels[app.status]}
              </span>
              <Button
                type="button"
                variant="ghost"
                size="icon"
                aria-label="Delete App"
                title={
                  ["unconfigured", "stopped", "failed"].includes(app.status)
                    ? "Delete App"
                    : "Stop the App before deleting it"
                }
                disabled={busy || !["unconfigured", "stopped", "failed"].includes(app.status)}
                onClick={() => void deleteApp()}
                className="size-8 text-[var(--error)] hover:text-[var(--error)]"
              >
                <Trash2 className="size-3.5" />
              </Button>
            </>
          )}
        </div>

        {error && <Notice error>{error}</Notice>}
        {operationMessage && <Notice>{operationMessage}</Notice>}

        {flow.stage === "working" && !(
          (app?.status === "running" || app?.status === "testing") &&
          (app.kind === "desktop" || Boolean(app.previewUrl))
        ) ? (
          <AppMountProgress
            flow={flow}
            completedEvents={completedEvents}
            lastEvent={lastEvent}
            lastTool={lastTool}
            onOpenTerminal={() => onFocusTerminal(flow.session.terminalSessionId)}
            busy={busy}
            onCancel={() => void cancelAgentWork(flow.session)}
          />
        ) : flow.stage === "failed" ? (
          <AppMountFailure
            failure={flow}
            busy={busy}
            onAskAI={() => {
              if (!flow.session) return void askAI()
              const task = repairTask(
                flow.details,
                app,
                candidates,
                runtime?.app.id === app?.id ? runtime : null,
                logs,
              )
              setFlow({ stage: "confirm", session: flow.session, task })
            }}
            onOpenTerminal={() => flow.session && onFocusTerminal(flow.session.terminalSessionId)}
            onManual={() => app ? setEditor("edit") : openManual()}
            onRetry={() => flow.session
              ? setFlow({ stage: "confirm", session: flow.session, task: mountTask })
              : void askAI()}
          />
        ) : apps.length === 0 && !editor ? (
          <UnconfiguredApp
            flow={flow}
            busy={busy}
            terminalSessions={terminalSessions}
            onAskAgent={(shell) => void askAgent(shell)}
            onChooseAgent={(session) => setFlow({ stage: "confirm", session, task: mountTask })}
            onCreateAgent={createAgent}
            onConfirmTask={(session, task) => void sendTask(session, task)}
            onOpenTerminal={chooseTerminal}
            onChooseTerminal={(session) => {
              setFlow({ stage: "waiting-terminal", session })
              onFocusTerminal(session.sessionId)
            }}
            onCreateTerminal={openManualTerminal}
            onFocusTerminal={onFocusTerminal}
            onDetect={detectTerminal}
            onAttach={attachTerminal}
            onManual={openManual}
            onCancel={() => setFlow({ stage: "idle" })}
          />
        ) : editor === "candidates" ? (
          <CandidatePicker
            candidates={candidates}
            busy={busy}
            onMount={mountCandidate}
            onReview={openManual}
            onManual={() => openManual()}
            onCancel={() => setEditor(null)}
          />
        ) : !app || editor || needsConfiguration ? (
          <AppForm
            key={app && (editor === "edit" || needsConfiguration) ? app.id : "new"}
            project={project}
            app={editor === "edit" || needsConfiguration ? app : undefined}
            busy={busy}
            suggestion={editor === "new" ? manualSuggestion : undefined}
            onCancel={editor === "new"
              ? () => {
                  setEditor(null)
                  setManualSuggestion(undefined)
                }
              : app && !needsConfiguration
                ? () => setEditor(null)
                : undefined}
            onSave={saveApp}
          />
        ) : (
          <ConfiguredApp
            app={app}
            runtime={runtime?.app.id === app.id ? runtime : null}
            logs={logs}
            automation={automation}
            automationResult={automationResult}
            busy={busy}
            automationBusy={automationBusy}
            viewport={viewport}
            onViewport={setViewport}
            onEdit={() => setEditor("edit")}
            onStart={() => run(() => projectService.startApp(app.id, context.experimentId))}
            onStop={() => run(() => projectService.stopApp(app.id, context.experimentId))}
            onRestart={() => run(() => projectService.restartApp(app.id, context.experimentId))}
            onFocus={() => run(
              () => projectService.focusApp(app.id),
              "Application focused.",
            )}
            onTest={() => run(
              () => projectService.runAppTests(app.id, context.experimentId),
              "Tests finished successfully.",
            )}
            onSetPreview={(url) => run(
              () => projectService.setPreviewURL(app.id, url, context.experimentId),
              "Preview URL updated.",
            )}
            onSmoke={() => runAutomation(() => projectService.smokeTestApp(app.id))}
            onHealthcheck={() => runAutomation(() => projectService.waitForAppHealthcheck(app.id))}
            onCapture={() => runAutomation(() => projectService.captureAppPreview(app.id))}
            onConsole={() => runAutomation(() => projectService.getAppConsoleErrors(app.id))}
          />
        )}
      </div>
    </section>
  )
}

function UnconfiguredApp({
  flow,
  busy,
  terminalSessions,
  onAskAgent,
  onChooseAgent,
  onCreateAgent,
  onConfirmTask,
  onOpenTerminal,
  onChooseTerminal,
  onCreateTerminal,
  onFocusTerminal,
  onDetect,
  onAttach,
  onManual,
  onCancel,
}: {
  flow: AppFlow
  busy: boolean
  terminalSessions: AppTerminalSession[]
  onAskAgent: (shell: "claude" | "codex") => void
  onChooseAgent: (session: AgentSession) => void
  onCreateAgent: (shell: "claude" | "codex") => void
  onConfirmTask: (session: AgentSession, task: string) => void
  onOpenTerminal: () => void
  onChooseTerminal: (session: ProjectTerminalSession) => void
  onCreateTerminal: (shell: TerminalShell) => void
  onFocusTerminal: (sessionId: string) => void
  onDetect: (session: ProjectTerminalSession) => void
  onAttach: (session: ProjectTerminalSession, url: string, detection?: ManagedDetection) => void
  onManual: (candidate?: AppCandidate) => void
  onCancel: () => void
}) {
  const [task, setTask] = useState(mountTask)
  const [manualUrl, setManualUrl] = useState("")

  useEffect(() => {
    if (flow.stage === "confirm") setTask(flow.task)
  }, [flow])

  if (flow.stage === "choose-agent") {
    return (
      <CenteredPanel title={flow.sessions.length ? "Choose an AI session" : "Open an agent"} onCancel={onCancel}>
        {flow.sessions.length > 0 ? (
          <div className="mt-5 grid gap-2">
            {flow.sessions.map((session) => (
              <button key={session.id} type="button" onClick={() => onChooseAgent(session)} className={choiceClass}>
                <Bot className="size-4 text-[var(--primary)]" />
                <span className="min-w-0 flex-1 text-left">
                  <span className="block truncate text-xs font-medium">{agentName(session.agent)}</span>
                  <span className="block truncate text-[0.65rem] text-[var(--on-surface-variant)]">{session.name || "Session"} · {sessionStatusLabel(session.status)}</span>
                </span>
                <ChevronDown className="size-4 -rotate-90" />
              </button>
            ))}
          </div>
        ) : (
          <div className="mt-5 flex flex-wrap justify-center gap-2">
            <Button type="button" onClick={() => onCreateAgent("claude")} disabled={busy} className="rounded-full">
              <BrandChip brand="claude" className="size-5 rounded-md" iconClassName="size-3" /> Open Claude Code
            </Button>
            <Button type="button" variant="outline" onClick={() => onCreateAgent("codex")} disabled={busy} className="rounded-full border-[var(--outline-variant)]">
              <BrandChip brand="codex" className="size-5 rounded-md" iconClassName="size-3" /> Open Codex
            </Button>
          </div>
        )}
      </CenteredPanel>
    )
  }

  if (flow.stage === "confirm") {
    return (
      <CenteredPanel title="Confirm the task for the AI" onCancel={onCancel}>
        <p className="mt-1 text-xs text-[var(--on-surface-variant)]">Will be sent to {agentName(flow.session.agent)} · {flow.session.name || "Active session"}.</p>
        <textarea value={task} onChange={(event) => setTask(event.target.value)} aria-label="Task for the agent" className="mt-4 h-72 w-full resize-none rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] p-3 font-mono text-[0.7rem] leading-5 outline-none focus-visible:ring-2 focus-visible:ring-[var(--ring)]" />
        <div className="mt-4 flex justify-end gap-2">
          <Button type="button" variant="ghost" onClick={onCancel}>Cancel</Button>
          <Button type="button" onClick={() => onConfirmTask(flow.session, task)} disabled={busy || !task.trim()}>{busy && <LoaderCircle className="size-4 animate-spin" />} Send task</Button>
        </div>
      </CenteredPanel>
    )
  }

  if (flow.stage === "choose-terminal") {
    return (
      <CenteredPanel title="Open terminal" onCancel={onCancel}>
        {flow.sessions.length > 0 && (
          <div className="mt-5 grid gap-2">
            <p className="text-xs text-[var(--on-surface-variant)]">Existing terminals</p>
            {flow.sessions.map((session) => (
              <button key={session.sessionId} type="button" onClick={() => onChooseTerminal(session)} className={choiceClass}>
                <TerminalSquare className="size-4 text-[var(--primary)]" />
                <span className="min-w-0 flex-1 truncate text-left text-xs">{session.name || sessionLabel(session)} · {sessionStatusLabel(session.status)}</span>
              </button>
            ))}
          </div>
        )}
        <div className="mt-5 flex flex-wrap justify-center gap-2">
          {(["cmd", "wsl", "claude", "codex"] as const).map((shell) => (
            <Button key={shell} type="button" variant="outline" onClick={() => onCreateTerminal(shell)} disabled={busy}>New {shell === "cmd" ? "CMD" : shell === "wsl" ? "WSL" : agentName(shell)}</Button>
          ))}
        </div>
      </CenteredPanel>
    )
  }

  if (flow.stage === "waiting-terminal") {
    const detected = flow.detection?.candidates.find((candidate) => candidate.managed)
    return (
      <CenteredPanel title="Waiting for you to start the application…" onCancel={onCancel}>
        <p className="mt-1 text-xs text-[var(--on-surface-variant)]">Seizen will only check the tree managed by the {sessionLabel(flow.session)} terminal.</p>
        {detected && (
          <div className="mt-5 rounded-xl bg-[var(--primary-container)] p-3 text-xs text-[var(--on-primary-container)]">
            <p>We detected an application at {detected.url}</p>
            <div className="mt-3 flex gap-2">
              <Button type="button" onClick={() => onAttach(flow.session, detected.url, flow.detection)} disabled={busy}>Link to Seizen</Button>
              <Button type="button" variant="ghost" onClick={() => onCancel()}>Ignore</Button>
            </div>
          </div>
        )}
        {flow.detection?.diagnostic && <p className="mt-3 text-xs text-[var(--on-surface-variant)]">{flow.detection.diagnostic}</p>}
        <div className="mt-5 flex flex-wrap justify-center gap-2">
          <Button type="button" onClick={() => onDetect(flow.session)} disabled={busy}>{busy ? <LoaderCircle className="size-4 animate-spin" /> : <Activity className="size-4" />} Detect now</Button>
          <Button type="button" variant="outline" onClick={() => onFocusTerminal(flow.session.sessionId)}><TerminalSquare className="size-4" /> Open terminal</Button>
          <Button type="button" variant="ghost" onClick={() => onManual()}>Manual configuration</Button>
        </div>
        <form className="mt-4 flex gap-2" onSubmit={(event) => { event.preventDefault(); onAttach(flow.session, manualUrl) }}>
          <Input value={manualUrl} onChange={(event) => setManualUrl(event.target.value)} aria-label="URL to link" placeholder="http://localhost:5173" />
          <Button type="submit" variant="outline" disabled={busy || !manualUrl.trim()}>Link URL</Button>
        </form>
        <Button type="button" variant="ghost" onClick={onCancel} className="mt-3">Cancel waiting</Button>
      </CenteredPanel>
    )
  }

  return (
    <div className="m-auto flex w-full max-w-2xl flex-col items-center px-6 py-12 text-center">
      <h2 className="text-xl font-semibold tracking-[-0.03em]">No app mounted here yet</h2>
      <p className="mt-3 max-w-xl text-sm leading-6 text-[var(--on-surface-variant)]">Ask your AI to detect, configure, and run the project, or start the application from a terminal and link it once it's ready.</p>

      <div className="mt-8 grid w-full max-w-lg gap-3 sm:grid-cols-2">
        <button
          type="button"
          onClick={() => onAskAgent("claude")}
          disabled={busy}
          className="flex items-center gap-3 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 text-left shadow-[0_1px_3px_var(--shadow-soft)] outline-none transition-[box-shadow,transform] hover:shadow-[0_1px_3px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.99] disabled:opacity-50"
        >
          <BrandChip brand="claude" className="size-10 rounded-xl" iconClassName="size-5" />
          <span className="min-w-0">
            <span className="block text-sm font-semibold">Claude Code</span>
            <span className="block text-[0.68rem] leading-4 text-[var(--on-surface-variant)]">
              Detects, configures, and runs your app
            </span>
          </span>
        </button>
        <button
          type="button"
          onClick={() => onAskAgent("codex")}
          disabled={busy}
          className="flex items-center gap-3 rounded-2xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 text-left shadow-[0_1px_3px_var(--shadow-soft)] outline-none transition-[box-shadow,transform] hover:shadow-[0_1px_3px_var(--shadow-soft),0_10px_28px_var(--shadow-elevated)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] active:scale-[0.99] disabled:opacity-50"
        >
          <BrandChip brand="codex" className="size-10 rounded-xl" iconClassName="size-5" />
          <span className="min-w-0">
            <span className="block text-sm font-semibold">Codex · ChatGPT</span>
            <span className="block text-[0.68rem] leading-4 text-[var(--on-surface-variant)]">
              Mounts the project with OpenAI's AI
            </span>
          </span>
        </button>
      </div>

      <div className="mt-4 flex flex-wrap justify-center gap-2">
        <Button type="button" variant="outline" onClick={onOpenTerminal} disabled={busy} className="rounded-full border-[var(--outline-variant)]"><TerminalSquare className="size-4" /> Open terminal</Button>
        <Button type="button" variant="ghost" onClick={() => onManual()} className="rounded-full text-[var(--on-surface-variant)]">Manual configuration</Button>
      </div>

      <p className="mt-8 text-[0.68rem] text-[var(--on-surface-variant)]">Seizen will show the preview once it detects a port or a running desktop application.</p>
      {terminalSessions.some((session) => session.status === "running") && <span className="sr-only">Managed terminals are available</span>}
    </div>
  )
}

function CandidatePicker({
  candidates,
  busy,
  onMount,
  onReview,
  onManual,
  onCancel,
}: {
  candidates: AppCandidate[]
  busy: boolean
  onMount: (candidate: AppCandidate) => void
  onReview: (candidate: AppCandidate) => void
  onManual: () => void
  onCancel: () => void
}) {
  return (
    <CenteredPanel title="Add App" onCancel={onCancel}>
      <p className="mt-1 text-xs text-[var(--on-surface-variant)]">Choose a detected candidate or configure another App manually.</p>
      <div className="mt-5 grid gap-2">
        {candidates.map((candidate) => (
          <div key={candidate.id} className="flex flex-wrap items-center gap-3 rounded-xl bg-[var(--surface-container)] px-3 py-2">
            <div className="min-w-0 flex-1">
              <p className="truncate text-xs font-medium">{candidate.name} · {candidate.framework || candidate.kind}</p>
              <p className="mt-0.5 truncate font-mono text-[0.65rem] text-[var(--on-surface-variant)]">{candidate.startCommand || candidate.executable || candidate.reason}</p>
            </div>
            {canMountCandidate(candidate) && <Button type="button" variant="outline" onClick={() => onMount(candidate)} disabled={busy}>Mount this App</Button>}
            <Button type="button" variant="ghost" onClick={() => onReview(candidate)}>Review configuration</Button>
          </div>
        ))}
      </div>
      <div className="mt-4 flex justify-end">
        <Button type="button" variant="ghost" onClick={onManual}>Manual configuration</Button>
      </div>
    </CenteredPanel>
  )
}

function AppMountProgress({
  flow,
  completedEvents,
  lastEvent,
  lastTool,
  busy,
  onOpenTerminal,
  onCancel,
}: {
  flow: Extract<AppFlow, { stage: "working" }>
  completedEvents: Set<string>
  lastEvent: string
  lastTool: string
  busy: boolean
  onOpenTerminal: () => void
  onCancel: () => void
}) {
  return (
    <CenteredPanel title={`${agentName(flow.session.agent)} is mounting the App`}>
      <div className="mt-6 grid gap-3">
        {progressSteps.map(([label, startedEvent, completedEvent]) => {
          const complete = completedEvents.has(completedEvent)
          const active = !complete && (lastEvent === startedEvent || completedEvents.has(startedEvent))
          return (
            <div key={label} className="flex items-center gap-3 text-left text-xs">
              {complete ? <CheckCircle2 className="size-4 text-[var(--primary)]" /> : active ? <LoaderCircle className="size-4 animate-spin text-[var(--primary)]" /> : <Circle className="size-4 text-[var(--outline)]" />}
              <span className={cn(!complete && !active && "text-[var(--on-surface-variant)]")}>{label}</span>
            </div>
          )
        })}
      </div>
      <div className="mt-6 rounded-xl bg-[var(--surface-container)] p-3 text-left text-[0.68rem] text-[var(--on-surface-variant)]">
        <p>Last actual step: {lastEvent ? mountEventLabel(lastEvent) : "Task sent"}</p>
        <p className="mt-1">Last MCP tool: {lastTool ? toolLabel(lastTool) : "None yet"}</p>
      </div>
      <div className="mt-5 flex justify-end gap-2">
        <Button type="button" variant="outline" onClick={onOpenTerminal}><TerminalSquare className="size-4" /> Open terminal</Button>
        <Button type="button" variant="ghost" onClick={onCancel} disabled={busy}>{busy && <LoaderCircle className="size-4 animate-spin" />}<X className="size-4" /> Cancel</Button>
      </div>
    </CenteredPanel>
  )
}

function AppMountFailure({
  failure,
  busy,
  onAskAI,
  onOpenTerminal,
  onManual,
  onRetry,
}: {
  failure: Extract<AppFlow, { stage: "failed" }>
  busy: boolean
  onAskAI: () => void
  onOpenTerminal: () => void
  onManual: () => void
  onRetry: () => void
}) {
  return (
    <CenteredPanel title="We couldn't mount the App">
      <dl className="mt-5 grid gap-2 rounded-xl bg-[var(--error-container)] p-3 text-left text-xs text-[var(--on-error-container)]">
        <div><dt className="font-medium">Step</dt><dd>{failure.details.step}</dd></div>
        <div><dt className="font-medium">Last error</dt><dd>{failure.details.error}</dd></div>
        {failure.details.exitCode !== undefined && <div><dt className="font-medium">Exit code</dt><dd>{failure.details.exitCode}</dd></div>}
        {failure.details.expectedPort !== undefined && <div><dt className="font-medium">Expected port</dt><dd>{failure.details.expectedPort}</dd></div>}
        {failure.details.logs && <div><dt className="font-medium">Last lines</dt><dd><pre className="mt-1 max-h-32 overflow-auto whitespace-pre-wrap font-mono text-[0.68rem]">{failure.details.logs}</pre></dd></div>}
        {failure.session && <div><dt className="font-medium">Agent</dt><dd>{agentName(failure.session.agent)}</dd></div>}
      </dl>
      <div className="mt-5 flex flex-wrap justify-end gap-2">
        <Button type="button" onClick={onAskAI} disabled={busy}><Bot className="size-4" /> Ask the AI to fix it</Button>
        <Button type="button" variant="outline" onClick={onOpenTerminal} disabled={!failure.session}><TerminalSquare className="size-4" /> Open terminal</Button>
        <Button type="button" variant="ghost" onClick={onManual}>Edit configuration</Button>
        <Button type="button" variant="ghost" onClick={onRetry}><RotateCw className="size-4" /> Retry</Button>
      </div>
    </CenteredPanel>
  )
}

function CenteredPanel({
  title,
  onCancel,
  children,
}: {
  title: string
  onCancel?: () => void
  children: ReactNode
}) {
  return (
    <div className="panel-in m-auto w-full max-w-2xl rounded-[1.5rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_1px_3px_var(--shadow-soft),0_12px_36px_var(--shadow-elevated)]">
      <div className="flex items-center gap-3">
        <h2 className="min-w-0 flex-1 text-lg font-semibold tracking-[-0.03em]">{title}</h2>
        {onCancel && <Button type="button" variant="ghost" size="icon" aria-label="Close" onClick={onCancel} className="rounded-full"><X className="size-4" /></Button>}
      </div>
      {children}
    </div>
  )
}

function AppForm({
  project,
  app,
  suggestion,
  busy,
  onCancel,
  onSave,
}: {
  project: Project
  app?: ProjectApp
  suggestion?: AppCandidate
  busy: boolean
  onCancel?: () => void
  onSave: (input: AppInput) => Promise<void>
}) {
  const [kind, setKind] = useState<AppKind>(app?.kind ?? suggestion?.kind ?? "web")
  const [name, setName] = useState(app?.name ?? suggestion?.name ?? project.name)
  const [workingDirectory, setWorkingDirectory] = useState(app?.workingDirectory ?? suggestion?.workingDirectory ?? project.path)
  const [startCommand, setStartCommand] = useState(app?.startCommand ?? suggestion?.startCommand ?? "")
  const [stopCommand, setStopCommand] = useState(app?.stopCommand ?? "")
  const [testCommand, setTestCommand] = useState(app?.testCommand ?? suggestion?.testCommand ?? "")
  const [executable, setExecutable] = useState(app?.executable ?? suggestion?.executable ?? "")
  const [argumentsJson, setArgumentsJson] = useState(app?.argumentsJson ?? "[]")
  const [previewUrl, setPreviewUrl] = useState(app?.previewUrl ?? (suggestion?.expectedPorts[0] ? String(suggestion.expectedPorts[0]) : ""))
  const [healthcheckUrl, setHealthcheckUrl] = useState(app?.healthcheckUrl ?? suggestion?.suggestedHealthcheck ?? "")
  const [validation, setValidation] = useState<Record<string, string>>({})

  const submit = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    const errors: Record<string, string> = {}
    if (!name.trim()) errors.name = "Enter a name"
    errors.workingDirectory = validateWorkingDirectory(project.path, workingDirectory)
    if (kind === "web" && !startCommand.trim()) errors.startCommand = "Enter the start command"
    if (kind === "desktop" && !executable.trim() && !startCommand.trim()) errors.executable = "Enter the executable or a start command"
    for (const [field, command] of [["startCommand", startCommand], ["stopCommand", stopCommand], ["testCommand", testCommand]] as const) {
      if (/[\0\r\n]/.test(command)) errors[field] = "Use a single-line command"
    }
    let argumentsValue: unknown
    try {
      argumentsValue = JSON.parse(argumentsJson || "[]")
      if (!Array.isArray(argumentsValue) || argumentsValue.some((item) => typeof item !== "string")) {
        errors.argumentsJson = "Use a JSON array of strings"
      }
    } catch {
      errors.argumentsJson = "Arguments must be valid JSON"
    }
    let preview = ""
    let healthcheck = ""
    try { preview = normalizeAppPreviewURL(previewUrl) } catch (caught) { errors.previewUrl = errorMessage(caught) }
    try { healthcheck = normalizeAppPreviewURL(healthcheckUrl) } catch (caught) { errors.healthcheckUrl = errorMessage(caught) }
    for (const key of Object.keys(errors)) if (!errors[key]) delete errors[key]
    setValidation(errors)
    if (Object.keys(errors).length) return
    try {
      await onSave({
        projectId: project.id,
        name: name.trim(),
        kind,
        workingDirectory: workingDirectory.trim(),
        startCommand: startCommand.trim(),
        stopCommand: stopCommand.trim(),
        testCommand: testCommand.trim(),
        executable: executable.trim(),
        argumentsJson: argumentsJson || "[]",
        previewUrl: preview,
        healthcheckUrl: healthcheck,
      })
    } catch (caught) {
      const message = errorMessage(caught)
      setValidation({ [appFieldForError(message)]: message })
    }
  }

  return (
    <form onSubmit={submit} className="m-auto w-full max-w-2xl rounded-[1.5rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_12px_36px_var(--shadow-elevated)]">
      <h2 className="text-lg font-semibold tracking-[-0.03em]">Manual configuration</h2>
      <span className="sr-only">Configure App</span>
      <p className="mt-1 text-xs text-[var(--on-surface-variant)]">Seizen will use this configuration to run and verify the application.</p>

      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <Field label="Type">
          <Select value={kind} onChange={(event) => setKind(event.target.value as AppKind)}>
            <option value="web">Web</option>
            <option value="desktop">Desktop</option>
          </Select>
        </Field>
        <Field label="Name" error={validation.name}><Input required aria-invalid={Boolean(validation.name)} value={name} onChange={(event) => setName(event.target.value)} /></Field>
        <Field label="Working directory" wide error={validation.workingDirectory}><Input required aria-invalid={Boolean(validation.workingDirectory)} value={workingDirectory} onChange={(event) => setWorkingDirectory(event.target.value)} /></Field>
        <Field label="Start command" wide={kind === "web"} error={validation.startCommand}><Input required={kind === "web"} aria-invalid={Boolean(validation.startCommand)} value={startCommand} onChange={(event) => setStartCommand(event.target.value)} placeholder="npm run dev" /></Field>
        {kind === "desktop" && (
          <>
            <Field label="Executable" error={validation.executable}><Input aria-invalid={Boolean(validation.executable)} value={executable} onChange={(event) => setExecutable(event.target.value)} placeholder="app.exe" /></Field>
            <Field label="JSON arguments" wide error={validation.argumentsJson}><Input aria-invalid={Boolean(validation.argumentsJson)} value={argumentsJson} onChange={(event) => setArgumentsJson(event.target.value)} placeholder='["--dev"]' /></Field>
          </>
        )}
        <Field label="Test command" error={validation.testCommand}><Input aria-invalid={Boolean(validation.testCommand)} value={testCommand} onChange={(event) => setTestCommand(event.target.value)} placeholder="npm test" /></Field>
        <Field label="Stop command" error={validation.stopCommand}><Input aria-invalid={Boolean(validation.stopCommand)} value={stopCommand} onChange={(event) => setStopCommand(event.target.value)} /></Field>
        <Field label="Preview port or URL" error={validation.previewUrl}><Input aria-invalid={Boolean(validation.previewUrl)} value={previewUrl} onChange={(event) => setPreviewUrl(event.target.value)} placeholder="3000 or http://localhost:3000" /></Field>
        <Field label="Healthcheck URL" error={validation.healthcheckUrl}><Input aria-invalid={Boolean(validation.healthcheckUrl)} value={healthcheckUrl} onChange={(event) => setHealthcheckUrl(event.target.value)} placeholder="http://localhost:3000/health" /></Field>
      </div>
      {validation.general && <p role="alert" className="mt-4 text-xs text-[var(--error)]">{validation.general}</p>}
      <div className="mt-5 flex justify-end gap-2">
        {onCancel && <Button type="button" variant="ghost" onClick={onCancel}>Cancel</Button>}
        <Button type="submit" disabled={busy}>{busy && <LoaderCircle className="size-4 animate-spin" />} Save App</Button>
      </div>
    </form>
  )
}

function ConfiguredApp({
  app,
  runtime,
  logs,
  automation,
  automationResult,
  busy,
  automationBusy,
  viewport,
  onViewport,
  onEdit,
  onStart,
  onStop,
  onRestart,
  onFocus,
  onTest,
  onSetPreview,
  onSmoke,
  onHealthcheck,
  onCapture,
  onConsole,
}: {
  app: ProjectApp
  runtime: AppRuntimeStatus | null
  logs: string
  automation: BrowserAutomationStatus | null
  automationResult: BrowserAutomationResult | null
  busy: boolean
  automationBusy: boolean
  viewport: Viewport
  onViewport: (value: Viewport) => void
  onEdit: () => void
  onStart: () => void
  onStop: () => void
  onRestart: () => void
  onFocus: () => void
  onTest: () => void
  onSetPreview: (url: string) => void
  onSmoke: () => void
  onHealthcheck: () => void
  onCapture: () => void
  onConsole: () => void
}) {
  const [previewInput, setPreviewInput] = useState(app.previewUrl)
  const [previewError, setPreviewError] = useState("")
  useEffect(() => setPreviewInput(app.previewUrl), [app.previewUrl])

  const previewUrl = useMemo(() => {
    if (!app.previewUrl) return ""
    try { return normalizeBrowserURL(app.previewUrl) } catch { return "" }
  }, [app.previewUrl])
  const changing = busy || ["starting", "stopping"].includes(app.status)
  const running = app.status === "running" || app.status === "testing"
  const verified = Boolean(runtime?.processAlive && (!app.healthcheckUrl || runtime.healthcheckPassed))

  const savePreview = () => {
    try {
      const normalized = normalizeAppPreviewURL(previewInput)
      if (!normalized) throw new Error("Enter the preview URL")
      setPreviewError("")
      onSetPreview(normalized)
    } catch (caught) {
      setPreviewError(errorMessage(caught))
    }
  }

  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[1.35rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] shadow-[0_12px_36px_var(--shadow-elevated)]">
      <div className="flex min-h-14 flex-wrap items-center gap-2 border-b border-[var(--outline-variant)] px-4 py-2">
        <div className="min-w-0 flex-1">
          <h2 className="truncate text-sm font-semibold">{app.name}</h2>
          <p className="truncate text-[0.65rem] text-[var(--on-surface-variant)]">{app.kind === "web" ? app.startCommand : app.executable}</p>
        </div>
        {runtime?.processAlive && (
          <span className="flex items-center gap-1 rounded-full bg-[var(--surface-container)] px-2.5 py-1 text-[0.65rem] text-[var(--on-surface-variant)]">
            <Activity className="size-3" /> PID {runtime.pid}
          </span>
        )}
        {app.kind === "desktop" && running && runtime?.processAlive && (
          <Button
            type="button"
            variant="ghost"
            onClick={onFocus}
            disabled={busy}
            className="rounded-full"
          >
            <ExternalLink className="size-3.5" /> Open or focus
          </Button>
        )}
        <Button type="button" variant="ghost" onClick={onEdit} disabled={changing || running} className="rounded-full"><Pencil className="size-3.5" /> Edit</Button>
        {app.status === "starting" ? (
          <Button type="button" variant="ghost" onClick={onStop} disabled={busy} className="rounded-full"><Square className="size-3.5" /> Cancel</Button>
        ) : !running && app.status !== "stopping" ? (
          <Button type="button" onClick={onStart} disabled={changing} className="rounded-full"><Play className="size-3.5" /> Run</Button>
        ) : running ? (
          <>
            <Button type="button" variant="ghost" onClick={onTest} disabled={busy || !app.testCommand || app.status === "testing"} title={!app.testCommand ? "Set a test command" : undefined} className="rounded-full"><FlaskConical className="size-3.5" /> Tests</Button>
            <Button type="button" variant="ghost" onClick={onRestart} disabled={busy || app.status === "testing"} className="rounded-full"><RotateCw className="size-3.5" /> Restart</Button>
            <Button type="button" variant="ghost" onClick={onStop} disabled={busy || app.status === "testing"} className="rounded-full"><Square className="size-3.5" /> Stop</Button>
          </>
        ) : null}
      </div>

      {app.kind === "web" && running ? (
        <div className="flex min-h-0 flex-1 flex-col bg-[var(--surface-container)] p-3">
          <div className="mb-2 flex flex-wrap items-center gap-1">
            {(["desktop", "tablet", "mobile"] as const).map((size) => {
              const Icon = size === "desktop" ? Monitor : size === "tablet" ? Tablet : Smartphone
              const label = size === "desktop" ? "Desktop" : size === "tablet" ? "Tablet" : "Mobile"
              return (
                <Tooltip key={size}>
                  <TooltipTrigger asChild>
                    <button
                      type="button"
                      onClick={() => onViewport(size)}
                      aria-label={`Viewport ${label}`}
                      aria-pressed={viewport === size}
                      className={cn(
                        "flex size-8 items-center justify-center rounded-lg text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
                        viewport === size && "bg-[var(--primary-container)] text-[var(--on-primary-container)] hover:bg-[var(--primary-container)] hover:text-[var(--on-primary-container)]",
                      )}
                    >
                      <Icon className="size-4" />
                    </button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom">{label}</TooltipContent>
                </Tooltip>
              )
            })}
            <div className="ml-2 flex min-w-[16rem] flex-1 items-center gap-1">
              <Input aria-label="Preview URL" value={previewInput} onChange={(event) => setPreviewInput(event.target.value)} className="h-8 min-w-0 text-xs" placeholder="http://localhost:3000" />
              {previewInput !== app.previewUrl && (
                <Tooltip>
                  <TooltipTrigger asChild>
                    <Button type="button" variant="ghost" size="icon" aria-label="Save preview URL" onClick={savePreview} disabled={busy} className="size-8"><Save className="size-4" /></Button>
                  </TooltipTrigger>
                  <TooltipContent side="bottom">Save preview URL</TooltipContent>
                </Tooltip>
              )}
            </div>
            <span className={cn("flex items-center gap-1 px-2 text-[0.65rem]", verified ? "text-[var(--primary)]" : "text-[var(--on-surface-variant)]")}>
              {verified ? <CheckCircle2 className="size-3.5" /> : <LoaderCircle className="size-3.5 animate-spin" />}
              {verified ? (app.healthcheckUrl ? "Healthcheck verified" : "Process verified") : "Verifying"}
            </span>
            {previewUrl && (
              <Tooltip>
                <TooltipTrigger asChild>
                  <Button type="button" variant="ghost" size="icon" aria-label="Open outside Seizen" onClick={() => BrowserOpenURL(previewUrl)} className="size-8"><ExternalLink className="size-4" /></Button>
                </TooltipTrigger>
                <TooltipContent side="bottom">Open outside Seizen</TooltipContent>
              </Tooltip>
            )}
          </div>
          {previewError && <p role="alert" className="mb-2 text-xs text-[var(--error)]">{previewError}</p>}
          <div className="relative mx-auto min-h-[300px] flex-1 overflow-hidden rounded-xl border border-[var(--outline-variant)] bg-white transition-[width] duration-300 ease-[cubic-bezier(.22,1,.36,1)]" style={{ width: viewportWidths[viewport], maxWidth: "100%" }}>
            {previewUrl ? <iframe src={previewUrl} title={`Preview of ${app.name}`} sandbox="allow-forms allow-same-origin allow-scripts" referrerPolicy="no-referrer" className="absolute inset-0 size-full border-0" /> : <EmptyPreview />}
          </div>
          <AutomationBar
            status={automation}
            result={automationResult}
            busy={automationBusy}
            hasPreview={Boolean(previewUrl)}
            onSmoke={onSmoke}
            onHealthcheck={onHealthcheck}
            onCapture={onCapture}
            onConsole={onConsole}
          />
          <Logs value={logs} />
        </div>
      ) : (
        <div className="flex min-h-0 flex-1 flex-col">
          <div className="m-auto max-w-lg px-6 py-10 text-center">
            {changing && <LoaderCircle className="mx-auto mb-4 size-7 animate-spin text-[var(--primary)]" />}
            <h3 className="text-base font-semibold">{statusLabels[app.status]}</h3>
            <p className="mt-2 text-xs leading-5 text-[var(--on-surface-variant)]">
              {app.kind === "desktop"
                ? `${app.executable || "Unconfigured executable"} · ${app.workingDirectory}`
                : `${app.startCommand || "Unconfigured command"} · ${app.workingDirectory}`}
            </p>
            {app.kind === "desktop" && running && runtime && (
              <p className="mt-3 text-xs text-[var(--on-surface-variant)]">Process {runtime.processAlive ? "active" : "unverified"} · PID {runtime.pid || "—"}</p>
            )}
          </div>
          <Logs value={logs} />
        </div>
      )}
    </div>
  )
}

function AutomationBar({
  status,
  result,
  busy,
  hasPreview,
  onSmoke,
  onHealthcheck,
  onCapture,
  onConsole,
}: {
  status: BrowserAutomationStatus | null
  result: BrowserAutomationResult | null
  busy: boolean
  hasPreview: boolean
  onSmoke: () => void
  onHealthcheck: () => void
  onCapture: () => void
  onConsole: () => void
}) {
  return (
    <div className="mt-2 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-3 py-2">
      <div className="flex flex-wrap items-center gap-2">
        <span className="mr-auto text-[0.65rem] text-[var(--on-surface-variant)]">{status?.message ?? "Checking web automation…"}</span>
        <Button type="button" variant="ghost" onClick={onSmoke} disabled={busy || !hasPreview} className="h-8 rounded-full text-xs"><Activity className="size-3.5" /> Smoke test</Button>
        <Button type="button" variant="ghost" onClick={onHealthcheck} disabled={busy || !hasPreview} className="h-8 rounded-full text-xs"><CheckCircle2 className="size-3.5" /> Healthcheck</Button>
        {status?.browserFeatures && (
          <>
            <Button type="button" variant="ghost" onClick={onCapture} disabled={busy || !hasPreview} className="h-8 rounded-full text-xs"><Camera className="size-3.5" /> Capture</Button>
            <Button type="button" variant="ghost" onClick={onConsole} disabled={busy || !hasPreview} className="h-8 rounded-full text-xs"><TerminalSquare className="size-3.5" /> Console</Button>
          </>
        )}
        {busy && <LoaderCircle className="size-4 animate-spin text-[var(--primary)]" />}
      </div>
      {result && (
        <div className={cn("mt-2 text-[0.68rem]", result.success ? "text-[var(--on-surface-variant)]" : "text-[var(--error)]")}>
          <p>{automationResultMessage(result)}</p>
          {result.screenshotPath && (
            <p className="mt-1 flex flex-wrap items-center gap-2 break-all">
              Capture: {result.screenshotPath}
              <button
                type="button"
                className="font-medium text-[var(--primary)] underline-offset-2 hover:underline"
                onClick={() => BrowserOpenURL(localFileURL(result.screenshotPath!))}
              >
                Open capture
              </button>
            </p>
          )}
          {result.consoleErrors && (
            result.consoleErrors.length > 0
              ? <ul className="mt-1 list-disc pl-4">{result.consoleErrors.map((item, index) => <li key={`${index}-${item}`}>{item}</li>)}</ul>
              : <p className="mt-1">No console errors.</p>
          )}
        </div>
      )}
    </div>
  )
}

function Logs({ value }: { value: string }) {
  return (
    <details className="group mt-2 shrink-0 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)]">
      <summary className="flex cursor-pointer list-none items-center gap-2 rounded-xl px-3 py-2 text-xs text-[var(--on-surface-variant)] outline-none transition-colors hover:bg-[var(--state-layer)] hover:text-[var(--on-surface)] focus-visible:ring-2 focus-visible:ring-[var(--ring)] [&::-webkit-details-marker]:hidden">
        <ChevronDown className="size-3.5 -rotate-90 transition-transform group-open:rotate-0" strokeWidth={1.8} />
        App logs
      </summary>
      <pre className="max-h-44 overflow-auto border-t border-[var(--outline-variant)] px-3 py-2 font-mono text-[0.68rem] leading-5 text-[var(--on-surface)]">
        {value || <span className="italic text-[var(--on-surface-variant)]">No logs yet.</span>}
      </pre>
    </details>
  )
}

function Notice({ error, children }: { error?: boolean; children: ReactNode }) {
  return (
    <p role={error ? "alert" : "status"} className={cn("view-enter mb-4 flex items-center gap-2 rounded-xl px-3 py-2 text-xs", error ? "bg-[var(--error-container)] text-[var(--on-error-container)]" : "bg-[var(--primary-container)] text-[var(--on-primary-container)]")}>
      {error ? <CircleAlert className="size-4 shrink-0" /> : <CheckCircle2 className="size-4 shrink-0" />} {children}
    </p>
  )
}

function Field({ label, wide, error, children }: { label: string; wide?: boolean; error?: string; children: ReactNode }) {
  return <label className={cn("grid gap-1.5 text-xs text-[var(--on-surface-variant)]", wide && "sm:col-span-2")}>{label}{children}{error && <span role="alert" className="text-[0.68rem] text-[var(--error)]">{error}</span>}</label>
}

function ViewLoading({ label }: { label: string }) {
  return <div className="overlay-in absolute inset-0 flex items-center justify-center bg-[var(--surface)] text-xs text-[var(--on-surface-variant)]"><LoaderCircle className="mr-2 size-4 animate-spin" />{label}</div>
}

function EmptyPreview() {
  return (
    <div className="flex size-full flex-col items-center justify-center gap-3 bg-[var(--surface)] text-xs text-[var(--on-surface-variant)]">
      <span className="flex size-10 items-center justify-center rounded-2xl bg-[var(--surface-container)]">
        <Monitor className="size-[1.15rem]" strokeWidth={1.6} />
      </span>
      Waiting for verified preview
    </div>
  )
}

function automationResultMessage(result: BrowserAutomationResult) {
  if (!result.available) return result.errorMessage || result.message || "The operation requires Playwright."
  if (!result.success) return result.errorMessage || result.message || "Automation failed."
  const status = result.statusCode ? ` · HTTP ${result.statusCode}` : ""
  return `${result.message || "Check completed"}${status} · ${result.durationMs} ms`
}

function localFileURL(path: string) {
  const normalized = path.replaceAll("\\", "/")
  const segments = normalized.split("/").map((segment, index) =>
    index === 0 ? segment : encodeURIComponent(segment),
  )
  return `file:///${segments.join("/")}`
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function validateWorkingDirectory(projectPath: string, value: string) {
  const directory = value.trim()
  if (!directory) return "Enter the working directory"
  const normalized = directory.replaceAll("\\", "/")
  if (normalized === ".." || normalized.startsWith("../")) {
    return "The directory must be inside the project"
  }
  if (!/^(?:[a-z]:\/|\/)/i.test(normalized)) return ""
  const root = projectPath.replaceAll("\\", "/").replace(/\/$/, "").toLowerCase()
  const absolute = normalized.replace(/\/$/, "").toLowerCase()
  return absolute === root || absolute.startsWith(`${root}/`)
    ? ""
    : "The directory must be inside the project"
}

function appFieldForError(message: string) {
  const value = message.toLowerCase()
  if (value.includes("directory")) return "workingDirectory"
  if (value.includes("argument")) return "argumentsJson"
  if (value.includes("healthcheck")) return "healthcheckUrl"
  if (value.includes("preview") || value.includes("url")) return "previewUrl"
  if (value.includes("executable")) return "executable"
  if (value.includes("command")) return "startCommand"
  if (value.includes("name")) return "name"
  return "general"
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

function belongsToProject(payload: unknown, projectId: string) {
  if (!isRecord(payload)) return true
  const payloadProjectId = payload.projectId ?? payload.project_id
  return typeof payloadProjectId !== "string" || payloadProjectId === projectId
}

function stringValue(value: unknown, fallback = "") {
  return typeof value === "string" ? value : fallback
}

function numberValue(value: unknown) {
  return typeof value === "number" && Number.isFinite(value) ? value : undefined
}

function agentName(value: string) {
  return value.toLowerCase().includes("claude")
    ? "Claude Code"
    : value.toLowerCase().includes("codex")
      ? "Codex"
      : value
}

function sessionLabel(session: ProjectTerminalSession) {
  return session.agent ? agentName(session.agent) : session.shell.toUpperCase()
}

function candidateInput(
  projectId: string,
  candidate: AppCandidate,
  previewUrl = "",
): AppInput {
  const port = candidate.expectedPorts[0]
  return {
    projectId,
    name: candidate.name,
    kind: candidate.kind,
    workingDirectory: candidate.workingDirectory,
    startCommand: candidate.startCommand,
    stopCommand: "",
    testCommand: candidate.testCommand,
    executable: candidate.executable,
    argumentsJson: "[]",
    previewUrl: previewUrl || (port ? `http://localhost:${port}/` : ""),
    healthcheckUrl: candidate.suggestedHealthcheck,
  }
}

function canMountCandidate(candidate: AppCandidate) {
  return candidate.confidence >= 0.8 && Boolean(
    candidate.kind === "web"
      ? candidate.startCommand.trim()
      : candidate.executable.trim() || candidate.startCommand.trim(),
  )
}

function repairTask(
  details: MountFailure,
  app: ProjectApp | undefined,
  candidates: AppCandidate[],
  runtime: AppRuntimeStatus | null,
  currentLogs: string,
) {
  const configuration = app ? {
    id: app.id,
    name: app.name,
    kind: app.kind,
    workingDirectory: app.workingDirectory,
    startCommand: app.startCommand,
    stopCommand: app.stopCommand,
    testCommand: app.testCommand,
    executable: app.executable,
    previewUrl: app.previewUrl,
    healthcheckUrl: app.healthcheckUrl,
    status: app.status,
  } : null
  const candidateContext = candidates.map((candidate) => ({
    id: candidate.id,
    name: candidate.name,
    kind: candidate.kind,
    framework: candidate.framework,
    workingDirectory: candidate.workingDirectory,
    startCommand: candidate.startCommand,
    testCommand: candidate.testCommand,
    executable: candidate.executable,
    expectedPorts: candidate.expectedPorts,
    confidence: candidate.confidence,
    reason: candidate.reason,
  }))
  const runtimeContext = runtime ? {
    runtimeReference: runtime.runtimeReference,
    pid: runtime.pid,
    processAlive: runtime.processAlive,
    healthcheckPassed: runtime.healthcheckPassed,
  } : null
  const safeLogs = redactForAgent(details.logs || currentLogs).slice(-8_000)
  return `Fix the App mount in this same session using Seizen's MCP tools.

Step that failed: ${details.step}
Error: ${redactForAgent(details.error)}
Exit code: ${details.exitCode ?? "not available"}
Expected port: ${details.expectedPort ?? "not available"}
Last logs:
${safeLogs || "not available"}

Current configuration (without arguments or secrets):
${redactForAgent(JSON.stringify(configuration, null, 2))}

Detected candidates:
${redactForAgent(JSON.stringify(candidateContext, null, 2))}

Observed runtime:
${JSON.stringify(runtimeContext, null, 2)}

Available tools: seizen_project_context, seizen_app_discover, seizen_app_mount, seizen_app_wait_ready, seizen_app_attach_running, seizen_app_get_runtime_diagnostics, seizen_app_run_tests, and seizen_app_get_console_errors.

Get seizen_project_context and seizen_app_get_runtime_diagnostics again. Do not start the main process outside AppRuntimeManager. Fix the configuration, remount, wait for readiness, run the smoke test, and check console errors.`
}

function redactForAgent(value: string) {
  return value
    .replace(/([?&](?:token|key|secret|password)=)[^&\s]+/gi, "$1[redacted]")
    .replace(/\b([a-z0-9_]*(?:token|password|secret|api[_-]?key)[a-z0-9_]*)\s*[:=]\s*\S+/gi, "$1=[redacted]")
    .replace(/(https?:\/\/)[^\s/@:]+:[^\s/@]+@/gi, "$1[redacted-credentials]@")
}

const choiceClass = "flex min-h-12 w-full items-center gap-3 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] px-3 py-2 outline-none transition-colors hover:bg-[var(--state-layer)] focus-visible:ring-2 focus-visible:ring-[var(--ring)]"
