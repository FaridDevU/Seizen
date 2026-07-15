import {
  BackupProject,
  ChooseDirectory,
  CloneRepository,
  CreateProject,
  DeleteProject,
  DetectDuplicateGroups,
  ExportProject,
  GetProjectThumbnail,
  GetProjectRoot,
  GroupDuplicate,
  UngroupDuplicate,
  ImportProjects,
  Initialize,
  ListProjects,
  OpenProject,
  RemoveProjectFromLibrary,
  RenameProject,
  ResizeProjectTerminal,
  SetArchived,
  SetFavorite,
  SetProjectGitHub,
  SetProjectRoot,
  StopProjectTerminal,
  WriteProjectTerminal,
  WriteProjectTerminalBinary,
} from "../../../wailsjs/go/core/App"
import type { TerminalShell } from "./workspace-model"

export type ProjectSource = "created" | "imported" | "git"
export type ProjectFilter = "all" | "favorites" | "archived"

export type AgentResourceSettings = {
  codexEnvironment: string
  claudeEnvironment: string
  opencodeEnvironment: string
  codexUnrestricted: boolean
  claudeUnrestricted: boolean
  opencodeUnrestricted: boolean
  sharedExtensions: boolean
}

export type ExperimentKind = "app" | "server"
export type ExperimentStatus =
  | "draft"
  | "creating"
  | "active"
  | "paused"
  | "awaiting_approval"
  | "review_ready"
  | "integrating"
  | "integrated"
  | "conflicted"
  | "failed"
  | "discarded"
  | "archived"

export type Experiment = {
  id: string
  projectId: string
  kind: ExperimentKind
  appId: string
  baseServerId: string | null
  name: string
  objective: string
  baseBranch: string
  branchName: string
  baseCommit: string
  worktreePath: string
  status: ExperimentStatus
  createdBy: "user" | "agent"
  agentSessionId: string | null
  riskLevel: "low" | "medium" | "high" | "critical"
  riskReasonsJson: string
  configurationJson: string
  createdAt: string
  updatedAt: string
  reviewReadyAt: string | null
  integratedAt: string | null
  discardedAt: string | null
}

export type ExperimentCreateInput = {
  projectId: string
  kind: ExperimentKind
  appId: string
  baseServerId: string
  name: string
  objective: string
  branchName: string
  createdBy: "user" | "agent"
  agentSessionId: string
  riskLevel: "low" | "medium" | "high" | "critical"
  riskReasonsJson: string
  configurationJson: string
  confirmed: boolean
}

export type ExperimentComparison = {
  experimentId: string
  baseCommit: string
  headCommit: string
  commitCount: number
  stat: string
  files: string
  patch: string
}

export type ExperimentReview = {
  experiment: Experiment
  comparison: ExperimentComparison
  testsPassed: boolean
  testOutput: string
  appVerified: boolean
  secretFindings: string[]
  conflicts: string[]
  integrationPath: string
  mainHead: string
  reproducibleVerified: boolean
}

export type ServerReproducibleExport = {
  experiment: Experiment
  server: ProjectServer
  files: string[]
  health: { healthy: boolean; message: string }
  services: ServerService[]
  connections: ServerConnection[]
  rebuilt: boolean
}

export type ProjectContext = {
  projectId: string
  experimentId: string
  name: string
  kind: ExperimentKind | "main"
  branchName: string
  path: string
  status: string
}

export type EditorSession = {
  sessionId: string
  projectId: string
  editorId: string
  url: string
}

export type Project = {
  id: string
  name: string
  path: string
  source: ProjectSource
  gitRemote: string | null
  branch: string | null
  favorite: boolean
  archived: boolean
  createdAt: string
  updatedAt: string
  groupId: string | null
  groupTitle: string | null
  variantLabel: string | null
}

export type ProjectCandidate = Pick<
  Project,
  "id" | "name" | "path" | "gitRemote"
>

export type WorkspacePhotoAsset = {
  assetId: string
  dataURL: string
}

export type DuplicateGroup = {
  key: string
  title: string
  variants: Array<{ projectId: string; label: string }>
}

export type AppKind = "web" | "desktop"
export type AppStatus =
  | "unconfigured"
  | "stopped"
  | "starting"
  | "running"
  | "testing"
  | "failed"
  | "stopping"

export type ProjectApp = {
  id: string
  projectId: string
  experimentId?: string
  name: string
  kind: AppKind
  workingDirectory: string
  startCommand: string
  stopCommand: string
  testCommand: string
  executable: string
  argumentsJson: string
  previewUrl: string
  healthcheckUrl: string
  status: AppStatus
  isPrimary: boolean
  createdAt: string
  updatedAt: string
}

export type AppInput = Omit<
  ProjectApp,
  "id" | "status" | "isPrimary" | "createdAt" | "updatedAt"
>

export type AppRuntimeStatus = {
  app: ProjectApp
  experimentId?: string
  runtimeReference: string
  pid: number
  processAlive: boolean
  healthcheckPassed: boolean
}

export type AppCandidate = {
  id: string
  name: string
  kind: AppKind
  workingDirectory: string
  startCommand: string
  testCommand: string
  executable: string
  expectedPorts: number[]
  suggestedHealthcheck: string
  framework: string
  confidence: number
  reason: string
  sourceFiles: string[]
}

export type ManagedAppDetection = {
  sessionId: string
  projectId: string
  spaceId: string
  candidates: Array<{
    pid: number
    port: number
    url: string
    managed: boolean
  }>
  diagnostic: string
}

export type AttachRunningAppInput = {
  projectId: string
  spaceId: string
  appId: string
  terminalSessionId: string
  previewUrl: string
  detectedPort: number
  discoverySource: "manual" | "detected" | "agent"
  confirmed: boolean
  name: string
  kind: AppKind
  workingDirectory: string
}

export type AgentSession = {
  id: string
  projectId: string
  experimentId?: string
  spaceId: string
  appId: string
  agent: string
  name: string
  status: string
  terminalSessionId: string
}

export type ProjectTerminalSession = {
  sessionId: string
  projectId: string
  experimentId?: string
  spaceId: string
  shell: string
  agent?: string
  status: string
  name: string
}

export type BrowserAutomationStatus = {
  provider: string
  available: boolean
  browserFeatures: boolean
  message: string
}

export type BrowserAutomationResult = {
  operation: string
  provider: string
  available: boolean
  success: boolean
  url?: string
  statusCode?: number
  durationMs: number
  screenshotPath?: string
  consoleErrors?: string[]
  message?: string
  errorMessage?: string
}

export type ServerProvider = "mock" | "wsl" | "incus"
export type ServerStatus =
  | "draft"
  | "provisioning"
  | "stopped"
  | "starting"
  | "running"
  | "degraded"
  | "stopping"
  | "failed"
  | "deleting"

export type ProjectServer = {
  id: string
  projectId: string
  appId: string
  experimentId: string | null
  baseServerId: string | null
  name: string
  provider: ServerProvider
  distro: string
  runtimeReference: string
  status: ServerStatus
  cpuLimit: number
  memoryMb: number
  diskGb: number
  keepAlive: boolean
  createdAt: string
  updatedAt: string
}

export type ServerStats = {
  cpuPercent: number
  memoryUsedMb: number
  memoryLimitMb: number
  limitsEnforced: boolean
  limitDescription: string
}

export type TopologySource = "declared" | "verified" | "observed"

export type ServerService = {
  id: string
  serverId: string
  name: string
  kind: string
  host: string
  port: number | null
  protocol: string
  healthcheckUrl: string
  status: string
  source: TopologySource
  metadataJson: string
  positionJson: string
}

export type ServerConnection = {
  id: string
  serverId: string
  sourceServiceId: string | null
  targetServiceId: string | null
  protocol: string
  port: number | null
  status: string
  source: TopologySource
  trafficRate: number
  errorRate: number
  metadataJson: string
}

export type TopologyHealthcheckResult = {
  sequenceId: string
  serverId: string
  serviceId: string
  connectionId: string
  healthy: boolean
  statusCode?: number
  durationMs: number
  message: string
  checkedAt: string
}

export type GlobalServer = ProjectServer & {
  projectName?: string
  appName?: string
  stats?: ServerStats
}

export type AgentApproval = {
  id: string
  sessionId: string
  projectId: string
  experimentId?: string
  appId?: string
  action: string
  resourceId: string
  requestJson: string
  status: "pending" | "approved" | "denied" | "consumed" | "expired"
  expiresAt: string
  createdAt: string
}

export type ServerInput = Omit<
  ProjectServer,
  "id" | "runtimeReference" | "status" | "createdAt" | "updatedAt"
> & { experimentId: string; baseServerId: string }

const available =
  typeof window !== "undefined" &&
  Boolean((window as Window & { go?: unknown }).go)
let initialization: Promise<void> | undefined

function callProjectBackend<T>(methodName: string, ...args: unknown[]) {
  const backend = (
    window as Window & {
      go?: { main?: { App?: Record<string, unknown> } }
    }
  ).go?.main?.App
  const method = backend?.[methodName]
  if (typeof method !== "function") {
    return Promise.reject(new Error(`${methodName} is not available`))
  }
  return (method as (...parameters: unknown[]) => Promise<T>)(...args)
}

function callAvailableProjectBackend<T>(
  methodNames: string[],
  ...args: unknown[]
) {
  const backend = (
    window as Window & {
      go?: { main?: { App?: Record<string, unknown> } }
    }
  ).go?.main?.App
  const methodName = methodNames.find(
    (candidate) => typeof backend?.[candidate] === "function",
  )
  if (!methodName) {
    return Promise.reject(
      new Error(`${methodNames[0]} is not available`),
    )
  }
  return (backend?.[methodName] as (...parameters: unknown[]) => Promise<T>)(
    ...args,
  )
}

export const projectService = {
  getAgentResourceSettings() {
    return callProjectBackend<AgentResourceSettings>("GetAgentResourceSettings")
  },

  setAgentResourceSettings(settings: AgentResourceSettings) {
    return callProjectBackend<AgentResourceSettings>(
      "SetAgentResourceSettings",
      settings,
    )
  },

  listExperiments(projectId: string, kind = "") {
    return callProjectBackend<Experiment[]>("ListExperiments", projectId, kind)
  },

  createExperiment(input: ExperimentCreateInput) {
    return callProjectBackend<Experiment>("CreateExperiment", input)
  },

  linkExperimentAgentSession(experimentId: string, sessionId: string) {
    return callProjectBackend<void>("LinkExperimentAgentSession", experimentId, sessionId)
  },

  compareExperiment(experimentId: string) {
    return callProjectBackend<ExperimentComparison>("CompareExperiment", experimentId)
  },

  prepareExperimentIntegration(experimentId: string) {
    return callProjectBackend<ExperimentReview>("PrepareExperimentIntegration", experimentId, true)
  },

  requestExperimentIntegration(experimentId: string) {
    return callProjectBackend<AgentApproval>("RequestExperimentIntegration", experimentId, true)
  },

  integrateExperiment(experimentId: string, approvalId: string) {
    return callProjectBackend<Experiment>("IntegrateExperiment", experimentId, approvalId, true)
  },

  discardExperiment(experimentId: string, backupDirty = false, deleteBranch = false) {
    return callProjectBackend<Experiment>("DiscardExperiment", {
      experimentId,
      confirmed: true,
      backupDirty,
      deleteBranch,
    })
  },

  archiveExperiment(experimentId: string) {
    return callProjectBackend<Experiment>("ArchiveExperiment", experimentId, true)
  },

  restoreExperiment(experimentId: string) {
    return callProjectBackend<Experiment>("RestoreExperiment", experimentId, true)
  },

  exportServerReproducibleConfig(experimentId: string, files: string[]) {
    return callProjectBackend<ServerReproducibleExport>(
      "ExportServerReproducibleConfig",
      experimentId,
      files,
      true,
    )
  },

  getProjectContext(projectId: string) {
    return callProjectBackend<ProjectContext>("GetProjectContext", projectId)
  },

  selectProjectExperiment(projectId: string, experimentId: string) {
    return callProjectBackend<ProjectContext>(
      "SelectProjectExperiment",
      projectId,
      experimentId,
    )
  },

  available,

  initialize() {
    if (!available) return
    if (!initialization) {
      initialization = Initialize().catch((error: unknown) => {
        initialization = undefined
        throw error
      })
    }
    return initialization
  },

  list(search = "", filter: ProjectFilter = "all") {
    return ListProjects(search, filter) as Promise<Project[]>
  },

  chooseDirectory(title: string) {
    return ChooseDirectory(title)
  },

  getProjectRoot() {
    return GetProjectRoot()
  },

  getProjectThumbnail(project: Project) {
    return GetProjectThumbnail(project.id, project.path)
  },

  getProjectWorkspace(project: Project, experimentId = "") {
    return callProjectBackend<string>(
      "GetProjectWorkspaceContext",
      project.id,
      experimentId,
    )
  },

  saveProjectWorkspace(project: Project, value: string, experimentId = "") {
    return callProjectBackend<void>(
      "SaveProjectWorkspaceContext",
      project.id,
      experimentId,
      value,
    )
  },

  getProjectWorkspaceBackground(project: Project) {
    return callProjectBackend<string>(
      "GetProjectWorkspaceBackground",
      project.id,
      project.path,
    )
  },

  chooseProjectWorkspaceBackground(project: Project) {
    return callProjectBackend<string>(
      "ChooseProjectWorkspaceBackground",
      project.id,
      project.path,
    )
  },

  clearProjectWorkspaceBackground(project: Project) {
    return callProjectBackend<void>(
      "ClearProjectWorkspaceBackground",
      project.id,
      project.path,
    )
  },

  chooseProjectWorkspacePhoto(project: Project) {
    return callProjectBackend<WorkspacePhotoAsset>(
      "ChooseProjectWorkspacePhoto",
      project.id,
      project.path,
    )
  },

  getProjectWorkspacePhoto(project: Project, assetId: string) {
    return callProjectBackend<string>(
      "GetProjectWorkspacePhoto",
      project.id,
      project.path,
      assetId,
    )
  },

  deleteProjectWorkspacePhoto(project: Project, assetId: string) {
    return callProjectBackend<void>(
      "DeleteProjectWorkspacePhoto",
      project.id,
      project.path,
      assetId,
    )
  },

  setProjectRoot(path: string) {
    return SetProjectRoot(path)
  },

  createProject(name: string) {
    return CreateProject(name) as Promise<Project>
  },

  importFolders(paths: string[]) {
    return ImportProjects(paths) as Promise<Project[]>
  },

  cloneRepository(url: string) {
    return CloneRepository(url) as Promise<Project>
  },

  deleteProject(project: Project) {
    return DeleteProject(project.id, project.path)
  },

  removeProjectFromLibrary(project: Project) {
    return RemoveProjectFromLibrary(project.id, project.path)
  },

  exportProject(project: Project) {
    return ExportProject(project.id, project.path)
  },

  setProjectGitHub(project: Project, url: string) {
    return SetProjectGitHub(project.id, project.path, url) as Promise<Project>
  },

  backupProject(project: Project) {
    return BackupProject(project.id, project.path)
  },

  startProjectTerminal(project: Project, shell: TerminalShell, experimentId = "") {
    return callProjectBackend<string>(
      "StartProjectTerminalContext",
      project.id,
      experimentId,
      shell,
    )
  },

  startProjectEditor(projectId: string, experimentId: string, editorId: string) {
    return callProjectBackend<EditorSession>(
      "StartProjectEditorContext",
      projectId,
      experimentId,
      editorId,
    )
  },

  startProjectAgentTerminal(
    project: Project,
    shell: Extract<TerminalShell, "codex" | "claude">,
    appId: string,
    experimentId = "",
  ) {
    return callProjectBackend<string>(
      "StartProjectAgentTerminalContext",
      project.id,
      experimentId,
      shell,
      appId,
    )
  },

  writeProjectTerminal(sessionID: string, input: string) {
    return WriteProjectTerminal(sessionID, input)
  },

  writeProjectTerminalBinary(sessionID: string, base64Input: string) {
    return WriteProjectTerminalBinary(sessionID, base64Input)
  },

  resizeProjectTerminal(sessionID: string, columns: number, rows: number) {
    return ResizeProjectTerminal(sessionID, columns, rows)
  },

  stopProjectTerminal(sessionID: string) {
    return StopProjectTerminal(sessionID)
  },

  renameProject(project: Project, newName: string) {
    return RenameProject(project.id, project.path, newName) as Promise<Project>
  },

  setFavorite(project: Project, favorite: boolean) {
    return SetFavorite(project.id, favorite) as Promise<Project>
  },

  setArchived(project: Project, archived: boolean) {
    return SetArchived(project.id, archived) as Promise<Project>
  },

  detectDuplicates(projects: Project[]) {
    const candidates: ProjectCandidate[] = projects.map(
      ({ id, name, path, gitRemote }) => ({ id, name, path, gitRemote }),
    )
    return DetectDuplicateGroups(
      candidates as Parameters<typeof DetectDuplicateGroups>[0],
    ) as Promise<DuplicateGroup[]>
  },

  groupDuplicate(group: DuplicateGroup) {
    return GroupDuplicate(
      group as Parameters<typeof GroupDuplicate>[0],
    ) as Promise<void>
  },

  ungroupDuplicate(groupId: string) {
    return UngroupDuplicate(groupId)
  },

  openProject(path: string) {
    return OpenProject(path)
  },

  listApps(projectId: string) {
    return callProjectBackend<ProjectApp[]>("ListApps", projectId)
  },

  discoverApps(projectId: string) {
    return callProjectBackend<AppCandidate[]>("DiscoverApps", projectId)
  },

  listProjectAgentSessions(projectId: string, spaceId = "workspace", experimentId = "") {
    return callProjectBackend<AgentSession[]>(
      "ListProjectAgentSessionsContext",
      projectId,
      experimentId,
      spaceId,
    )
  },

  sendAgentTask(
    projectId: string,
    sessionId: string,
    task: string,
    spaceId = "workspace",
    experimentId = "",
  ) {
    return callProjectBackend<void>(
      "SendAgentTaskContext",
      projectId,
      experimentId,
      spaceId,
      sessionId,
      task,
      true,
    )
  },

  cancelAgentTask(
    projectId: string,
    sessionId: string,
    spaceId = "workspace",
    experimentId = "",
  ) {
    return callProjectBackend<void>(
      "CancelAgentTaskContext",
      projectId,
      experimentId,
      spaceId,
      sessionId,
    )
  },

  listProjectTerminalSessions(projectId: string, spaceId = "workspace", experimentId = "") {
    return callProjectBackend<ProjectTerminalSession[]>(
      "ListProjectTerminalSessionsContext",
      projectId,
      experimentId,
      spaceId,
    )
  },

  detectManagedApp(_projectId: string, terminalSessionId: string) {
    return callProjectBackend<ManagedAppDetection>(
      "DetectTerminalApp",
      terminalSessionId,
    )
  },

  attachRunningApp(input: AttachRunningAppInput) {
    return callProjectBackend<AppRuntimeStatus>("AttachRunningApp", input)
  },

  createApp(input: AppInput) {
    return callProjectBackend<ProjectApp>("CreateApp", input)
  },

  updateApp(id: string, input: AppInput) {
    return callProjectBackend<ProjectApp>("UpdateApp", id, input)
  },

  setPrimaryApp(projectId: string, appId: string) {
    return callProjectBackend<ProjectApp>("SetPrimaryApp", projectId, appId)
  },

  deleteApp(id: string) {
    return callProjectBackend<void>("DeleteApp", id)
  },

  startApp(id: string, experimentId = "") {
    return callProjectBackend<ProjectApp>("StartAppContext", id, experimentId)
  },

  stopApp(id: string, experimentId = "") {
    return callProjectBackend<ProjectApp>("StopAppContext", id, experimentId)
  },

  restartApp(id: string, experimentId = "") {
    return callProjectBackend<ProjectApp>("RestartAppContext", id, experimentId)
  },

  getAppStatus(id: string, experimentId = "") {
    return callProjectBackend<AppRuntimeStatus>("GetAppStatusContext", id, experimentId)
  },

  setPreviewURL(id: string, url: string, experimentId = "") {
    return callProjectBackend<ProjectApp>("SetPreviewURLContext", id, experimentId, url)
  },

  runAppTests(id: string, experimentId = "") {
    return callProjectBackend<ProjectApp>("RunAppTestsContext", id, experimentId)
  },

  getAppLogs(id: string, experimentId = "") {
    return callProjectBackend<string>("GetAppLogsContext", id, experimentId)
  },

  cleanupProjectRuntime(projectId: string) {
    return callProjectBackend<void>("CleanupProjectRuntime", projectId)
  },

  cleanupProjectServers(projectId: string) {
    return callProjectBackend<void>("CleanupProjectServers", projectId)
  },

  getBrowserAutomationStatus(appId: string) {
    return callProjectBackend<BrowserAutomationStatus>(
      "GetBrowserAutomationStatus",
      appId,
    )
  },

  smokeTestApp(appId: string) {
    return callProjectBackend<BrowserAutomationResult>("SmokeTestApp", appId)
  },

  captureAppPreview(appId: string) {
    return callProjectBackend<BrowserAutomationResult>(
      "CaptureAppPreview",
      appId,
    )
  },

  getAppConsoleErrors(appId: string) {
    return callProjectBackend<BrowserAutomationResult>(
      "GetAppConsoleErrors",
      appId,
    )
  },

  testAppRoute(appId: string, route: string) {
    return callProjectBackend<BrowserAutomationResult>(
      "TestAppRoute",
      appId,
      route,
    )
  },

  waitForAppHealthcheck(appId: string, timeoutMs = 20_000) {
    return callProjectBackend<BrowserAutomationResult>(
      "WaitForAppHealthcheck",
      appId,
      timeoutMs,
    )
  },

  listServers(projectId: string, experimentId = "") {
    return callProjectBackend<ProjectServer[]>("ListServersContext", projectId, experimentId)
  },

  listAllServers() {
    return callProjectBackend<GlobalServer[]>("ListAllServers")
  },

  createServerDraft(input: ServerInput) {
    return callProjectBackend<ProjectServer>("CreateServerDraft", input)
  },

  startMockServer(id: string) {
    return callProjectBackend<ProjectServer>("StartMockServer", id)
  },

  stopMockServer(id: string) {
    return callProjectBackend<ProjectServer>("StopMockServer", id)
  },

  deleteServer(id: string) {
    return callProjectBackend<void>("DeleteServer", id)
  },

  startServer(id: string) {
    return callAvailableProjectBackend<ProjectServer>(
      ["StartServer", "StartMockServer"],
      id,
    )
  },

  stopServer(id: string) {
    return callAvailableProjectBackend<ProjectServer>(
      ["StopServer", "StopMockServer"],
      id,
    )
  },

  restartServer(id: string) {
    return callAvailableProjectBackend<ProjectServer>(["RestartServer"], id)
  },

  destroyServer(id: string) {
    return callAvailableProjectBackend<void>(
      ["DestroyServer", "DeleteServer"],
      id,
    )
  },

  getServerStats(id: string) {
    return callProjectBackend<ServerStats>("GetServerStats", id)
  },

  getServerLogs(id: string) {
    return callProjectBackend<string>("GetServerLogs", id)
  },

  startServerTerminal(id: string) {
    return callProjectBackend<string>("StartServerTerminal", id)
  },

  writeServerTerminal(sessionId: string, input: string) {
    return callProjectBackend<void>("WriteServerTerminal", sessionId, input)
  },

  resizeServerTerminal(sessionId: string, columns: number, rows: number) {
    return callProjectBackend<void>(
      "ResizeServerTerminal",
      sessionId,
      columns,
      rows,
    )
  },

  stopServerTerminal(sessionId: string) {
    return callProjectBackend<void>("StopServerTerminal", sessionId)
  },

  listServerServices(projectId: string, serverId: string) {
    return callProjectBackend<ServerService[]>(
      "ListServerServices",
      projectId,
      serverId,
    )
  },

  listServerConnections(projectId: string, serverId: string) {
    return callProjectBackend<ServerConnection[]>(
      "ListServerConnections",
      projectId,
      serverId,
    )
  },

  updateServicePosition(
    projectId: string,
    serverId: string,
    serviceId: string,
    positionJson: string,
  ) {
    return callProjectBackend<ServerService>(
      "UpdateServicePosition",
      projectId,
      serverId,
      serviceId,
      positionJson,
    )
  },

  verifyServerService(projectId: string, serverId: string, serviceId: string) {
    return callProjectBackend<ServerService>(
      "VerifyServerService",
      projectId,
      serverId,
      serviceId,
    )
  },

  verifyServerConnection(
    projectId: string,
    serverId: string,
    connectionId: string,
  ) {
    return callProjectBackend<ServerConnection>(
      "VerifyServerConnection",
      projectId,
      serverId,
      connectionId,
    )
  },

  runServerServiceHealthcheck(
    projectId: string,
    serverId: string,
    serviceId: string,
  ) {
    return callProjectBackend<TopologyHealthcheckResult>(
      "RunServerServiceHealthcheck",
      projectId,
      serverId,
      serviceId,
    )
  },

  focusApp(id: string) {
    return callProjectBackend<void>("FocusApp", id)
  },

  listPendingAgentApprovals(projectId: string) {
    return callProjectBackend<AgentApproval[]>(
      "ListPendingAgentApprovals",
      projectId,
    )
  },

  resolveAgentApproval(id: string, approved: boolean) {
    return callProjectBackend<AgentApproval>(
      "ResolveAgentApproval",
      id,
      approved,
    )
  },

  continueExperimentChangeOnPrincipal(id: string, advancedConfirmed: boolean) {
    return callProjectBackend<AgentApproval>("ContinueExperimentChangeOnPrincipal", id, advancedConfirmed)
  },
}
