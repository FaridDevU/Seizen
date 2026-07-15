import {
  useCallback,
  useEffect,
  useRef,
  useState,
  type FormEvent,
  type ReactNode,
} from "react"
import {
  AppWindow,
  CheckCircle2,
  CircleAlert,
  FileText,
  LoaderCircle,
  Network,
  Play,
  Plus,
  RotateCw,
  Server,
  Square,
  SquareTerminal,
  Trash2,
} from "lucide-react"
import { EventsOn } from "../../../wailsjs/runtime/runtime"

import { Button } from "@/components/ui/button"
import { confirmDialog, confirmWithOption, promptDialog } from "@/components/ui/confirm"
import { Select } from "@/components/ui/select"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/utils"
import { ServerDiagram } from "@/features/servers/ServerDiagram"

import {
  projectService,
  type AgentApproval,
  type Experiment,
  type ExperimentReview,
  type Project,
  type ProjectApp,
  type ProjectContext,
  type ProjectServer,
  type ServerInput,
  type ServerProvider,
  type ServerStatus,
} from "./project-service"
import { ExperimentComparison, ExperimentSelector } from "./ExperimentSelector"
import { ExperimentCreateDialog, type ExperimentDraft } from "./ExperimentCreateDialog"
import {
  RealTerminal,
  type RealTerminalHandle,
  type RealTerminalStatus,
} from "./RealTerminal"

type LabTab = "diagram" | "terminal" | "logs"

const statusLabels: Record<ServerStatus, string> = {
  draft: "Draft",
  provisioning: "Preparing",
  stopped: "Stopped",
  starting: "Starting",
  running: "Running",
  degraded: "Degraded",
  stopping: "Stopping",
  failed: "Failed",
  deleting: "Deleting",
}

const providerLabels: Record<ServerProvider, string> = {
  mock: "Test mock",
  wsl: "Local WSL server",
  incus: "Incus (not available)",
}

export function ServerLabView({
  project,
  context,
  onSelectExperiment,
  initialServerId,
  onOpenApp,
}: {
  project: Project
  context: ProjectContext
  onSelectExperiment: (experimentId: string) => Promise<void>
  initialServerId?: string
  onOpenApp: () => void
}) {
  const [apps, setApps] = useState<ProjectApp[]>([])
  const [servers, setServers] = useState<ProjectServer[]>([])
  const [baseServers, setBaseServers] = useState<ProjectServer[]>([])
  const [experiments, setExperiments] = useState<Experiment[]>([])
  const [selectedId, setSelectedId] = useState(initialServerId ?? "")
  const [tab, setTab] = useState<LabTab>("diagram")
  const [creating, setCreating] = useState(false)
  const [creatingExperiment, setCreatingExperiment] = useState(false)
  const [loading, setLoading] = useState(true)
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState("")
  const [approvals, setApprovals] = useState<AgentApproval[]>([])
  const [integrationApprovals, setIntegrationApprovals] = useState<AgentApproval[]>([])
  const [experimentReview, setExperimentReview] = useState<ExperimentReview | null>(null)
  const [comparisonText, setComparisonText] = useState("")
  const [operationMessage, setOperationMessage] = useState("")
  const [terminalServiceName, setTerminalServiceName] = useState("")

  const load = useCallback(async () => {
    try {
      const [nextApps, nextServers, nextBaseServers, nextApprovals, nextExperiments] = await Promise.all([
        projectService.listApps(project.id),
        projectService.listServers(project.id, context.experimentId),
        projectService.listServers(project.id, ""),
        projectService.listPendingAgentApprovals(project.id),
        projectService.listExperiments(project.id, "server"),
      ])
      const normalizedApps = nextApps ?? []
      const normalizedServers = nextServers ?? []
      setApps(normalizedApps)
      setServers(normalizedServers)
      setBaseServers(nextBaseServers ?? [])
      setApprovals((nextApprovals ?? []).filter(
        (approval) => approval.action !== "experiment.integrate" && (approval.experimentId ?? "") === context.experimentId,
      ))
      setIntegrationApprovals((nextApprovals ?? []).filter(
        (approval) => approval.action === "experiment.integrate" && approval.experimentId === context.experimentId,
      ))
      setExperiments(nextExperiments ?? [])
      setSelectedId((current) =>
        normalizedServers.some((server) => server.id === current)
          ? current
          : normalizedServers.some((server) => server.id === initialServerId)
            ? (initialServerId ?? "")
            : (normalizedServers[0]?.id ?? ""),
      )
      setError("")
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setLoading(false)
    }
  }, [context.experimentId, initialServerId, project.id])

  useEffect(() => {
    setLoading(true)
    setCreating(false)
    setTab("diagram")
    setTerminalServiceName("")
    setExperimentReview(null)
    setComparisonText("")
    setOperationMessage("")
    void load()
  }, [load])

  useEffect(() => {
    // Debounce: provisioning emits bursts; one reload per burst is enough.
    let timer: number | undefined
    const refresh = () => {
      window.clearTimeout(timer)
      timer = window.setTimeout(() => void load(), 150)
    }
    const subscriptions = [
      "agent.approval.requested",
      "agent.approval.resolved",
      "server.provisioning",
      "server.starting",
      "server.running",
      "server.degraded",
      "server.stopping",
      "server.stopped",
      "server.failed",
      "server.deleted",
      "experiment.created",
      "experiment.status.updated",
      "experiment.selected",
      "experiment.review.ready",
      "experiment.server.rebuild.started",
      "experiment.server.rebuild.verified",
    ].map((event) => EventsOn(event, refresh))
    return () => {
      window.clearTimeout(timer)
      subscriptions.forEach((off) => off())
    }
  }, [load])

  const act = async (action: () => Promise<unknown>) => {
    setBusy(true)
    setError("")
    try {
      await action()
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const createExperiment = async (draft: ExperimentDraft) => {
    setBusy(true)
    setError("")
    try {
      const experiment = await projectService.createExperiment({
        projectId: project.id,
        kind: "server",
        appId: draft.appId,
        baseServerId: draft.baseServerId,
        name: draft.name,
        objective: draft.objective,
        branchName: "",
        createdBy: "user",
        agentSessionId: "",
        riskLevel: "medium",
        riskReasonsJson: "[\"server infrastructure\"]",
        configurationJson: "",
        confirmed: true,
      })
      await onSelectExperiment(experiment.id)
      const sessionId = await projectService.startProjectAgentTerminal(project, draft.agent, draft.appId, experiment.id)
      await projectService.linkExperimentAgentSession(experiment.id, sessionId)
      setCreatingExperiment(false)
      await load()
    } catch (caught) {
      setError(errorMessage(caught))
    } finally {
      setBusy(false)
    }
  }

  const compareExperiment = () => act(async () => {
    if (!context.experimentId) return
    const comparison = await projectService.compareExperiment(context.experimentId)
    setComparisonText(`${comparison.stat || "No changes"}\n\n${comparison.files}`)
  })

  const prepareExperiment = () => act(async () => {
    if (!context.experimentId) return
    setExperimentReview(await projectService.prepareExperimentIntegration(context.experimentId))
  })

  const exportReproducible = async () => {
    if (!context.experimentId) return
    const value = await promptDialog({
      title: "Export reproducible configuration",
      message: "Declarative files related to the experiment, comma-separated.",
      initial: "seizen-rebuild.sh, Dockerfile, compose.yaml",
    })
    if (value === null) return
    const files = value.split(",").map((item) => item.trim()).filter(Boolean)
    if (!files.length) return
    const accepted = await confirmDialog({
      title: "Verify rebuild",
      message: "A new server will be rebuilt from these files and then destroyed.",
      confirmLabel: "Continue",
    })
    if (!accepted) return
    void act(async () => {
      const result = await projectService.exportServerReproducibleConfig(context.experimentId, files)
      setOperationMessage(`Server rebuilt and verified · ${result.files.join(", ")}`)
      setExperimentReview(null)
    })
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
    } catch (caught) {
      const message = errorMessage(caught)
      if (message.includes("uncommitted changes") && (await confirmDialog({
        title: "Create checkpoint",
        message: `${message}. Create a checkpoint before discarding it?`,
        confirmLabel: "Create checkpoint",
      }))) {
        await projectService.discardExperiment(context.experimentId, true, deleteBranch)
        await onSelectExperiment("")
      } else {
        setError(message)
      }
    } finally {
      setBusy(false)
    }
  }

  if (loading) {
    return (
      <div className="overlay-in absolute inset-0 flex items-center justify-center bg-[var(--surface)] text-xs text-[var(--on-surface-variant)]">
        <LoaderCircle className="mr-2 size-4 animate-spin" />
        Loading Server Lab
      </div>
    )
  }

  const server = servers.find((candidate) => candidate.id === selectedId)
  const linkedApp = apps.find((app) => app.id === server?.appId)
  const changing =
    server &&
    ["provisioning", "starting", "stopping", "deleting"].includes(
      server.status,
    )
  const active = server && ["running", "degraded"].includes(server.status)

  return (
    <section
      role="tabpanel"
      aria-label={`Server Lab for ${project.name}`}
      className="view-enter absolute inset-0 overflow-auto bg-[var(--surface)] px-5 pb-6 pt-20"
    >
      <div className="mx-auto flex min-h-full max-w-[1100px] flex-col">
        {servers.length > 0 && (
          <ExperimentSelector
            principalLabel="Main configuration"
            context={context}
            experiments={experiments}
            onSelect={(experimentId) => {
              setError("")
              void onSelectExperiment(experimentId).catch((caught: unknown) => setError(errorMessage(caught)))
            }}
            onNew={() => setCreatingExperiment(true)}
            onRestore={(experimentId) => act(async () => {
              const accepted = await confirmDialog({
                title: "Restore experiment",
                message: "The branch, worktree, and declarative server of this experiment will be restored.",
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
            kind="server"
            apps={apps}
            servers={baseServers}
            busy={busy}
            onCancel={() => setCreatingExperiment(false)}
            onCreate={createExperiment}
          />
        )}
        {approvals[0] && (
          <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-[var(--focus-border)] bg-[var(--primary-container)] px-4 py-3 text-xs text-[var(--on-primary-container)]">
            <div className="min-w-0 flex-1">
              <p className="font-semibold">The agent requests approval</p>
              <p className="mt-0.5 truncate opacity-80" title={approvals[0].requestJson}>
                {approvalLabel(approvals[0].action)}
              </p>
            </div>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={() => act(() => projectService.resolveAgentApproval(approvals[0].id, false))}
              className="rounded-full"
            >
              Reject
            </Button>
            <Button
              type="button"
              disabled={busy}
              onClick={() => act(() => projectService.resolveAgentApproval(approvals[0].id, true))}
              className="rounded-full"
            >
              Approve once
            </Button>
          </div>
        )}
        {context.experimentId && (
          <div className="mb-4 flex flex-wrap items-center gap-2 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container)] px-3 py-2 text-xs">
            <span className="mr-auto font-medium">{context.name} · {context.branchName}</span>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void compareExperiment()}>Compare</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void exportReproducible()}>Generate reproducible configuration</Button>
            <Button type="button" disabled={busy} onClick={() => void prepareExperiment()}>Integrate into Main</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => act(async () => {
              await projectService.archiveExperiment(context.experimentId)
              await onSelectExperiment("")
            })}>Keep experiment</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => void discardExperiment()} className="text-[var(--error)]">Discard</Button>
          </div>
        )}
        {operationMessage && (
          <p role="status" className="view-enter mb-4 flex items-center gap-2 rounded-xl bg-[var(--primary-container)] px-3 py-2 text-xs text-[var(--on-primary-container)]">
            <CheckCircle2 className="size-4 shrink-0" /> {operationMessage}
          </p>
        )}
        {comparisonText && (
          <ExperimentComparison text={comparisonText} onClose={() => setComparisonText("")} />
        )}
        {experimentReview && (
          <div className="mb-4 rounded-xl border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-4 text-xs">
            <p className="font-semibold">Review ready · {experimentReview.comparison.commitCount} commit(s)</p>
            <p className="mt-1 text-[var(--on-surface-variant)]">
              Tests: {experimentReview.testsPassed ? "passed" : "failed"} · App: {experimentReview.appVerified ? "verified" : "unverified"} · Server: {experimentReview.reproducibleVerified ? "rebuilt" : "not reproducible"}
            </p>
            {experimentReview.conflicts.length > 0 && <p className="mt-2 text-[var(--error)]">Conflicts: {experimentReview.conflicts.join(", ")}</p>}
            {experimentReview.secretFindings.length > 0 && <p className="mt-2 text-[var(--error)]">Possible secrets detected.</p>}
            {experimentReview.testsPassed && experimentReview.appVerified && experimentReview.reproducibleVerified && experimentReview.conflicts.length === 0 && (
              <Button type="button" className="mt-3" disabled={busy} onClick={() => act(() => projectService.requestExperimentIntegration(context.experimentId))}>Request final confirmation</Button>
            )}
          </div>
        )}
        {integrationApprovals[0] && (
          <div className="mb-4 flex flex-wrap items-center gap-3 rounded-xl border border-[var(--focus-border)] bg-[var(--primary-container)] px-4 py-3 text-xs text-[var(--on-primary-container)]">
            <span className="mr-auto font-semibold">The rebuild and temporary integration passed. Main has not been modified yet.</span>
            <Button type="button" disabled={busy} onClick={() => act(async () => {
              await projectService.resolveAgentApproval(integrationApprovals[0].id, true)
              await projectService.integrateExperiment(context.experimentId, integrationApprovals[0].id)
              await onSelectExperiment("")
            })}>Integrate into Main</Button>
            <Button type="button" variant="ghost" disabled={busy} onClick={() => act(() => projectService.resolveAgentApproval(integrationApprovals[0].id, false))}>Cancel</Button>
          </div>
        )}
        {error && (
          <p
            role="alert"
            className="view-enter mb-4 flex items-center gap-2 rounded-xl bg-[var(--error-container)] px-3 py-2 text-xs text-[var(--on-error-container)]"
          >
            <CircleAlert className="size-4 shrink-0" /> {error}
          </p>
        )}

        {apps.length === 0 ? (
          <div className="m-auto max-w-md text-center">
            <AppWindow className="mx-auto mb-4 size-8 text-[var(--primary)]" />
            <h2 className="text-lg font-semibold tracking-[-0.03em]">
              First set up an App
            </h2>
            <p className="mt-2 text-xs leading-5 text-[var(--on-surface-variant)]">
              Every server must be linked to an App in this project.
            </p>
            <Button
              type="button"
              onClick={onOpenApp}
              className="mt-5 rounded-full"
            >
              Open App view
            </Button>
          </div>
        ) : creating ? (
          <ServerDraftForm
            project={project}
            apps={apps}
            experimentId={context.experimentId}
            baseServerId={experiments.find((item) => item.id === context.experimentId)?.baseServerId ?? ""}
            busy={busy}
            onCancel={() => setCreating(false)}
            onCreate={(input) =>
              act(async () => {
                const created = await projectService.createServerDraft(input)
                setSelectedId(created.id)
                setCreating(false)
              })
            }
          />
        ) : !server ? (
          <div className="m-auto max-w-md text-center">
            <Server className="mx-auto mb-4 size-8 text-[var(--primary)]" />
            <h2 className="text-lg font-semibold tracking-[-0.03em]">
              Create a test server
            </h2>
            <p className="mt-2 text-xs leading-5 text-[var(--on-surface-variant)]">
              It will first be created as a draft. Provisioning only starts
              after confirming it.
            </p>
            <Button
              type="button"
              onClick={() => setCreating(true)}
              className="mt-5 rounded-full"
            >
              <Plus className="size-4" /> Create server
            </Button>
          </div>
        ) : (
          <>
            <header className="mb-3 flex flex-wrap items-center gap-2 rounded-[1.1rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] px-4 py-3">
              <Select
                value={selectedId}
                onChange={(event) => setSelectedId(event.target.value)}
                aria-label="Selected server"
                wrapperClassName="w-auto"
                className="min-w-44 rounded-full text-xs"
              >
                {servers.map((item) => (
                  <option key={item.id} value={item.id}>
                    {item.name}
                  </option>
                ))}
              </Select>
              <div className="min-w-0 flex-1">
                <p className="truncate text-xs font-semibold">
                  {linkedApp?.name ?? "App not available"} ·{" "}
                  {providerLabels[server.provider]}
                </p>
                <p className="text-[0.64rem] text-[var(--on-surface-variant)]">
                  {server.cpuLimit} CPU · {server.memoryMb} MB RAM ·{" "}
                  {server.diskGb} GB · {statusLabels[server.status]}
                </p>
              </div>
              {active ? (
                <Button
                  type="button"
                  variant="ghost"
                  disabled={busy || changing}
                  onClick={() => act(() => projectService.stopServer(server.id))}
                  className="rounded-full"
                >
                  <Square className="size-3.5" /> Stop
                </Button>
              ) : ["draft", "stopped", "failed"].includes(server.status) ? (
                <Button
                  type="button"
                  disabled={busy || changing}
                  onClick={() => act(() => projectService.startServer(server.id))}
                  className="rounded-full"
                >
                  <Play className="size-3.5" />
                  {server.status === "draft" ? "Confirm and start" : "Start"}
                </Button>
              ) : null}
              <Button
                type="button"
                variant="ghost"
                disabled={busy || changing || !active}
                onClick={() => act(() => projectService.restartServer(server.id))}
                className="rounded-full"
              >
                <RotateCw className="size-3.5" /> Restart
              </Button>
              <Button
                type="button"
                variant="ghost"
                disabled={
                  busy ||
                  changing ||
                  !["draft", "stopped", "failed"].includes(server.status)
                }
                aria-label="Delete server"
                title={
                  ["draft", "stopped", "failed"].includes(server.status)
                    ? "Delete server"
                    : "Stop the server before deleting it"
                }
                onClick={() => {
                  void confirmDialog({
                    title: "Delete server",
                    message: `${server.name} will be permanently deleted.`,
                    confirmLabel: "Delete",
                    tone: "danger",
                  }).then((accepted) => {
                    if (accepted) void act(() => projectService.destroyServer(server.id))
                  })
                }}
                className="rounded-full text-[var(--error)]"
              >
                <Trash2 className="size-3.5" />
              </Button>
              <Button
                type="button"
                variant="ghost"
                disabled={busy}
                onClick={() => setCreating(true)}
                className="rounded-full"
              >
                <Plus className="size-3.5" /> New
              </Button>
            </header>

            <div
              role="tablist"
              aria-label="Server Lab views"
              className="mb-3 flex w-fit items-center gap-1 rounded-full border border-[var(--outline-variant)] bg-[var(--surface-container)] p-1"
            >
              <LabTabButton active={tab === "diagram"} onClick={() => setTab("diagram")}>
                <Network className="size-3.5" /> Diagram
              </LabTabButton>
              <LabTabButton active={tab === "terminal"} onClick={() => {
                setTerminalServiceName("")
                setTab("terminal")
              }}>
                <SquareTerminal className="size-3.5" /> Root terminal
              </LabTabButton>
              <LabTabButton active={tab === "logs"} onClick={() => setTab("logs")}>
                <FileText className="size-3.5" /> Logs
              </LabTabButton>
            </div>

            <div
              key={tab}
              className="view-enter relative min-h-[420px] flex-1 overflow-hidden rounded-[1.35rem] border border-[var(--outline-variant)] bg-[var(--surface-container)]"
            >
              {tab === "diagram" ? (
                <ServerDiagram
                  projectId={project.id}
                  server={server}
                  onOpenTerminal={(service) => {
                    setTerminalServiceName(service?.name ?? "")
                    setTab("terminal")
                  }}
                  onOpenLogs={() => setTab("logs")}
                />
              ) : tab === "terminal" ? (
                <ServerTerminalPanel server={server} serviceName={terminalServiceName} />
              ) : (
                <ServerLogsPanel server={server} />
              )}
            </div>
          </>
        )}
      </div>
    </section>
  )
}

function ServerDraftForm({
  project,
  apps,
  experimentId,
  baseServerId,
  busy,
  onCancel,
  onCreate,
}: {
  project: Project
  apps: ProjectApp[]
  experimentId: string
  baseServerId: string
  busy: boolean
  onCancel: () => void
  onCreate: (input: ServerInput) => Promise<void>
}) {
  const [appId, setAppId] = useState(apps[0]?.id ?? "")
  const [name, setName] = useState("Local server")
  const [provider, setProvider] = useState<ServerProvider>("wsl")
  const [cpuLimit, setCpuLimit] = useState(2)
  const [memoryMb, setMemoryMb] = useState(2048)
  const [diskGb, setDiskGb] = useState(20)

  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    void onCreate({
      projectId: project.id,
      appId,
      experimentId,
      baseServerId,
      name: name.trim(),
      provider,
      distro: "Debian 12",
      cpuLimit,
      memoryMb,
      diskGb,
      keepAlive: false,
    })
  }

  return (
    <form
      onSubmit={submit}
      className="m-auto w-full max-w-xl rounded-[1.5rem] border border-[var(--outline-variant)] bg-[var(--surface-container-high)] p-5 shadow-[0_12px_36px_var(--shadow-elevated)]"
    >
      <h2 className="text-lg font-semibold tracking-[-0.03em]">
        Create server
      </h2>
      <p className="mt-1 text-xs text-[var(--on-surface-variant)]">
        A draft will be saved first without starting any processes.
      </p>
      <div className="mt-5 grid gap-4 sm:grid-cols-2">
        <Label text="Linked App">
          <Select
            required
            value={appId}
            onChange={(event) => setAppId(event.target.value)}
          >
            {apps.map((app) => (
              <option key={app.id} value={app.id}>
                {app.name}
              </option>
            ))}
          </Select>
        </Label>
        <Label text="Name">
          <Input
            required
            value={name}
            onChange={(event) => setName(event.target.value)}
          />
        </Label>
        <Label text="Provider">
          <Select
            value={provider}
            onChange={(event) => setProvider(event.target.value as ServerProvider)}
          >
            <option value="wsl">Local WSL server</option>
            <option value="mock">Test mock</option>
            <option value="incus" disabled>
              Incus (not available)
            </option>
          </Select>
        </Label>
        <Label text="Distribution">
          <Input value="Debian 12" disabled />
        </Label>
        <Label text="CPU">
          <Input
            type="number"
            min={1}
            max={16}
            value={cpuLimit}
            onChange={(event) => setCpuLimit(event.target.valueAsNumber)}
          />
        </Label>
        <Label text="RAM (MB)">
          <Input
            type="number"
            min={256}
            value={memoryMb}
            onChange={(event) => setMemoryMb(event.target.valueAsNumber)}
          />
        </Label>
        <Label text="Disk (GB)">
          <Input
            type="number"
            min={1}
            value={diskGb}
            onChange={(event) => setDiskGb(event.target.valueAsNumber)}
          />
        </Label>
      </div>
      <p className="mt-4 text-[0.68rem] text-[var(--on-surface-variant)]">
        Keep alive remains disabled until a visible tray process is
        available.
      </p>
      <div className="mt-5 flex justify-end gap-2">
        <Button type="button" variant="ghost" onClick={onCancel}>
          Cancel
        </Button>
        <Button type="submit" disabled={busy || !appId || !name.trim()}>
          {busy && <LoaderCircle className="size-4 animate-spin" />}
          Create draft
        </Button>
      </div>
    </form>
  )
}

function ServerTerminalPanel({ server, serviceName }: { server: ProjectServer; serviceName?: string }) {
  const [sessionId, setSessionId] = useState("")
  const [status, setStatus] = useState<RealTerminalStatus>("starting")
  const [error, setError] = useState("")
  const terminalRef = useRef<RealTerminalHandle | null>(null)
  const sessionRef = useRef("")
  const pendingRef = useRef(new Map<string, string>())
  const bufferedRef = useRef("")

  const write = (data: string) => {
    if (terminalRef.current) {
      terminalRef.current.write(data)
    } else {
      bufferedRef.current = (bufferedRef.current + data).slice(-128 * 1024)
    }
  }

  useEffect(() => {
    const offOutput = EventsOn("seizen:terminal-output", (payload: unknown) => {
      if (!isTerminalOutput(payload)) return
      if (payload.sessionId === sessionRef.current) {
        write(payload.data)
      } else if (!sessionRef.current) {
        const current = pendingRef.current.get(payload.sessionId) ?? ""
        pendingRef.current.set(
          payload.sessionId,
          (current + payload.data).slice(-128 * 1024),
        )
      }
    })
    const offExit = EventsOn("seizen:terminal-exit", (payload: unknown) => {
      if (!isTerminalExit(payload) || payload.sessionId !== sessionRef.current) {
        return
      }
      write(
        payload.error
          ? `\r\n[Terminal ended: ${payload.error}]\r\n`
          : "\r\n[Terminal ended]\r\n",
      )
      setStatus(payload.error ? "error" : "exited")
      setError(payload.error)
      sessionRef.current = ""
      setSessionId("")
    })
    return () => {
      offOutput()
      offExit()
      pendingRef.current.clear()
    }
  }, [])

  useEffect(() => {
    if (server.provider !== "wsl" || !["running", "degraded"].includes(server.status)) {
      setStatus("exited")
      return
    }

    let cancelled = false
    setStatus("starting")
    setError("")
    void projectService
      .startServerTerminal(server.id)
      .then((id) => {
        if (cancelled) {
          return projectService.stopServerTerminal(id).catch(() => undefined)
        }
        sessionRef.current = id
        setSessionId(id)
        setStatus("running")
        const pending = pendingRef.current.get(id)
        if (pending) write(pending)
        pendingRef.current.clear()
      })
      .catch((caught: unknown) => {
        if (!cancelled) {
          setStatus("error")
          setError(errorMessage(caught))
        }
      })

    return () => {
      cancelled = true
      const id = sessionRef.current
      sessionRef.current = ""
      if (id) void projectService.stopServerTerminal(id).catch(() => undefined)
    }
  }, [server.id, server.provider, server.status, serviceName])

  if (server.provider !== "wsl") {
    return (
      <EmptyPanel
        icon={SquareTerminal}
        title="Root terminal not available"
        message="The Debian root terminal only opens on Local WSL server; the Mock provider does not create a distribution."
      />
    )
  }

  if (!["running", "degraded"].includes(server.status)) {
    return (
      <EmptyPanel
        icon={SquareTerminal}
        title={`Root terminal for ${server.name}`}
        message="Turn on the server to connect the terminal securely."
      />
    )
  }

  return (
    <div className="absolute inset-0 flex flex-col bg-[#111513]">
      <div className="border-b border-white/10 px-3 py-2 text-[0.68rem] text-[#b5bdb7]">
        {serviceName ? `${serviceName} · root` : "root"} · {server.name} · {server.distro}
      </div>
      <RealTerminal
        sessionId={sessionId || undefined}
        status={status}
        error={error}
        autoFocus
        ariaLabel={serviceName ? `Terminal for ${serviceName} on ${server.name}` : `Root terminal for ${server.name}`}
        className="min-h-0 flex-1"
        onReady={(handle) => {
          terminalRef.current = handle
          if (handle && bufferedRef.current) {
            handle.write(bufferedRef.current)
            bufferedRef.current = ""
          }
        }}
        onData={(id, data) => projectService.writeServerTerminal(id, data)}
        onResize={(id, columns, rows) =>
          projectService.resizeServerTerminal(id, columns, rows)
        }
      />
    </div>
  )
}

function ServerLogsPanel({ server }: { server: ProjectServer }) {
  const [logs, setLogs] = useState("")
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState("")
  const logRef = useRef<HTMLPreElement>(null)
  const stickToBottomRef = useRef(true)

  useEffect(() => {
    if (stickToBottomRef.current && logRef.current) {
      logRef.current.scrollTop = logRef.current.scrollHeight
    }
  }, [logs])

  useEffect(() => {
    let active = true
    const refresh = () => projectService
        .getServerLogs(server.id)
        .then((value) => {
          if (active) {
            setLogs(value)
            setError("")
          }
        })
        .catch((caught: unknown) => {
          if (active) setError(errorMessage(caught))
        })
        .finally(() => {
          if (active) setLoading(false)
        })
    setLoading(true)
    void refresh()
    const timer = window.setInterval(() => void refresh(), 1_500)
    return () => {
      active = false
      window.clearInterval(timer)
    }
  }, [server.id])

  if (loading) {
    return (
      <div className="absolute inset-0 flex items-center justify-center text-xs text-[var(--on-surface-variant)]">
        <LoaderCircle className="mr-2 size-4 animate-spin" /> Loading logs
      </div>
    )
  }
  if (error) {
    return (
      <EmptyPanel
        icon={FileText}
        title="Logs not available"
        message={error}
      />
    )
  }
  if (!logs.trim()) {
    return (
      <EmptyPanel
        icon={FileText}
        title="No events recorded"
        message="Provisioning, health, and action events will appear here."
      />
    )
  }
  return (
    <pre
      ref={logRef}
      onScroll={(event) => {
        const target = event.currentTarget
        stickToBottomRef.current =
          target.scrollHeight - target.scrollTop - target.clientHeight < 40
      }}
      className="absolute inset-0 overflow-auto whitespace-pre-wrap p-4 font-mono text-xs leading-5 text-[var(--on-surface-variant)]"
    >
      {logs}
    </pre>
  )
}

function LabTabButton({
  active,
  children,
  onClick,
}: {
  active: boolean
  children: ReactNode
  onClick: () => void
}) {
  return (
    <button
      type="button"
      role="tab"
      aria-selected={active}
      onClick={onClick}
      className={cn(
        "flex h-8 items-center gap-1.5 rounded-full px-3 text-[0.7rem] font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-[var(--ring)]",
        active
          ? "bg-[var(--primary-container)] text-[var(--on-primary-container)]"
          : "text-[var(--on-surface-variant)] hover:bg-[var(--state-layer)]",
      )}
    >
      {children}
    </button>
  )
}

function EmptyPanel({
  icon: Icon,
  title,
  message,
}: {
  icon: typeof Server
  title: string
  message: string
}) {
  return (
    <div className="absolute inset-0 flex flex-col items-center justify-center px-6 text-center">
      <Icon className="size-6 text-[var(--primary)]" />
      <p className="mt-3 text-sm font-semibold">{title}</p>
      <p className="mt-1 max-w-md text-xs leading-5 text-[var(--on-surface-variant)]">
        {message}
      </p>
    </div>
  )
}

function Label({ text, children }: { text: string; children: ReactNode }) {
  return (
    <label className="grid gap-1.5 text-xs text-[var(--on-surface-variant)]">
      {text}
      {children}
    </label>
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

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null
}

function errorMessage(error: unknown) {
  return error instanceof Error ? error.message : String(error)
}

function approvalLabel(action: string) {
  const labels: Record<string, string> = {
    "server.create": "Create a local server",
    "server.create_draft": "Create a local server",
    "server.start": "Provision or start the server",
    "server.exec": "Run a sensitive root command",
    "server.destroy": "Delete the server",
    "server.network": "Change the network or open ports",
    "server.secret": "Inject a secret",
    "server.export_reproducible_config": "Rebuild and verify the reproducible configuration",
  }
  return labels[action] ?? action
}

