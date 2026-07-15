import assert from "node:assert/strict"
import { readFile } from "node:fs/promises"
import test from "node:test"

const source = (name) => readFile(new URL(`../src/${name}`, import.meta.url), "utf8")

test("selector integrates Workspace, App, and Server Lab without unmounting the canvas", async () => {
  const [selector, workspace] = await Promise.all([
    source("features/projects/ProjectModeSelector.tsx"),
    source("features/projects/ProjectWorkspace.tsx"),
  ])
  for (const label of ["Workspace", "App", "Server Lab"]) assert.match(selector, new RegExp(label))
  assert.match(selector, /role="tablist"/)
  assert.match(workspace, /invisible pointer-events-none/)
})

test("App and Server Lab share a contextual experiment selector", async () => {
  const [selector, appView, serverLab, workspace, service] = await Promise.all([
    source("features/projects/ExperimentSelector.tsx"),
    source("features/projects/AppView.tsx"),
    source("features/projects/ServerLabView.tsx"),
    source("features/projects/ProjectWorkspace.tsx"),
    source("features/projects/project-service.ts"),
  ])
  assert.match(selector, /<option value="">{principalLabel}<\/option>/)
  assert.match(selector, /<optgroup label="Experiments">/)
  for (const label of [
    "Preparing", "Active", "Paused", "Awaiting approval", "Ready to review",
    "Integrating", "Integrated", "Conflicted", "Failed", "Discarded", "Archived",
  ]) assert.match(selector, new RegExp(label))
  assert.match(appView, /principalLabel=/)
  assert.match(serverLab, /principalLabel="Main configuration"/)
  assert.match(workspace, /activeContext/)
  assert.match(workspace, /onSelectExperiment={selectExperiment}/)
  assert.match(service, /SelectProjectExperiment/)
  assert.match(selector, /onRestore/)
  for (const contract of ["Compare", "Integrate into Main", "Keep experiment", "Discard"]) {
    assert.match(appView, new RegExp(contract))
    assert.match(serverLab, new RegExp(contract))
  }
  assert.match(serverLab, /Generate reproducible/)
  assert.match(service, /ExportServerReproducibleConfig/)
  assert.match(service, /RestoreExperiment/)
})

test("the workspace adds panels from an ordered context menu", async () => {
  const workspace = await source("features/projects/ProjectWorkspace.tsx")
  assert.match(workspace, /onContextMenu=\{openWorkspaceMenu\}/)
  for (const group of ["AI", "Terminals", "Code editors", "Tools"]) {
    assert.match(workspace, new RegExp(`label="${group}"`))
  }
  assert.match(workspace, /aria-haspopup="menu"/)
  assert.match(workspace, /ChevronRight/)
  assert.match(workspace, /cursor-grab[^"]*active:cursor-grabbing/)
  assert.doesNotMatch(workspace, /interactionRef\.current\?\.kind === "pan"/)
  assert.doesNotMatch(workspace, /addMenuRef/)
})

test("the chat offers changing or restoring the workspace background", async () => {
  const [workspace, service] = await Promise.all([
    source("features/projects/ProjectWorkspace.tsx"),
    source("features/projects/project-service.ts"),
  ])
  assert.match(workspace, /aria-label="Workspace command bar"[\s\S]*<Plus/)
  assert.match(workspace, /Change background/)
  assert.match(workspace, /Default background/)
  for (const layer of ["image", "grid-fine", "grid-major", "panels"]) {
    assert.match(workspace, new RegExp(`data-workspace-layer="${layer}"`))
  }
  assert.match(workspace, /backgroundPosition: `\$\{viewportView\.x\}px \$\{viewportView\.y\}px`/)
  assert.match(workspace, /100 \* viewportView\.zoom/)
  assert.match(workspace, /isPanning/)
  assert.match(workspace, /onLostPointerCapture=\{endInteraction\}/)
  assert.doesNotMatch(workspace, /backgroundParallax|viewport\.x \* 0\.24/)
  for (const method of [
    "GetProjectWorkspaceBackground",
    "ChooseProjectWorkspaceBackground",
    "ClearProjectWorkspaceBackground",
  ]) assert.match(service, new RegExp(method))
})

test("Ctrl+C copies the selection and Ctrl+V pastes in every xterm terminal", async () => {
  const terminal = await source("features/projects/RealTerminal.tsx")
  assert.match(terminal, /attachCustomKeyEventHandler/)
  assert.match(terminal, /const key = event\.key\.toLowerCase\(\)/)
  assert.match(terminal, /wantsCopy && terminal\.hasSelection\(\)/)
  assert.match(terminal, /ClipboardSetText\(selection\)/)
  assert.match(terminal, /pasteFromClipboard\(\)/)
  assert.match(terminal, /ClipboardGetText/)
  assert.match(terminal, /navigator\.clipboard/)
  assert.match(terminal, /addEventListener\("contextmenu", clipboardOnRightClick, true\)/)
  assert.match(terminal, /terminal\.paste\(text\)/)
})

test("Tools opens a persistent player with real Spotify controls", async () => {
  const [workspace, model] = await Promise.all([
    source("features/projects/ProjectWorkspace.tsx"),
    source("features/projects/workspace-model.ts"),
  ])
  assert.match(workspace, /label="Tools"[\s\S]*Spotify player/)
  assert.match(workspace, /addPlayer/)
  assert.match(workspace, /find\(\(node\) => node\.type === "player"\)[\s\S]*bringToFront\(existing\.id\)/)
  assert.match(workspace, /<SpotifyPlayerPanel \/>/)
  assert.match(workspace, /GetSpotifyPlayback/)
  assert.match(workspace, /ControlSpotifyPlayback\(action\)/)
  assert.match(workspace, /window\.setTimeout\(poll, spotifyPollDelay\)/)
  assert.match(workspace, /active = false[\s\S]*window\.clearTimeout\(timer\)/)
  for (const action of ["previous", "toggle", "next"]) {
    assert.match(workspace, new RegExp(`requestPlayback\\("${action}"\\)`))
  }
  assert.equal(workspace.match(/requestPlayback\("refresh"\)/g)?.length, 2)
  assert.match(workspace, /error && \([\s\S]*requestPlayback\("refresh"\)/)
  assert.match(workspace, /const size = \{ width: 420, height: 190 \}/)
  assert.match(workspace, /artworkDataURL \? \([\s\S]*<img/)
  assert.match(workspace, /playback\.durationSeconds[\s\S]*role="progressbar"/)
  assert.match(workspace, /spotifyPlaybackTime\(position\)/)
  assert.match(model, /type StoredPlayerNode[\s\S]*type: "player"/)
  assert.match(model, /base\.width === 480 && base\.height === 330[\s\S]*width: 420, height: 190/)
})

test("Code editors opens VS Code and Tools keeps the Browser", async () => {
  const workspace = await source("features/projects/ProjectWorkspace.tsx")
  assert.match(workspace, /GetEditorIntegrations/)
  assert.match(workspace, /setTimeout\(loadEditorIntegrations, 1_000\)/)
  assert.match(workspace, /prepareEditorSession\("vscode"\)/)
  assert.match(workspace, /VS Code · Seizen/)
  assert.match(workspace, /editorIntegrations\s*\.filter\(\(editor\) => editor\.enabled\)/)
  assert.match(workspace, /!editor\.available/)
  assert.match(workspace, /projectService\.startProjectEditor\([\s\S]*activeContext\.experimentId,[\s\S]*editorId/)
  assert.match(workspace, /StopProjectEditor\(sessionId\)/)
  assert.match(workspace, /EventsOn\("seizen:editor-exit"/)
  assert.match(workspace, /pendingEditorExitsRef/)
  assert.match(workspace, /cleanupProjectRuntime\(project\.id\)[\s\S]*editorStartPromisesRef/)
  assert.match(workspace, /<EditorPanel node=\{node\} onRetry=\{\(\) => retryEditor\(node\)\}/)
  assert.match(workspace, /onClick=\{onRetry\}[\s\S]*Retry/)
  assert.match(workspace, /sandbox="[^"]*allow-scripts[^"]*"/)
  assert.match(workspace, /referrerPolicy="no-referrer"/)
  assert.match(workspace, /StartNativeEditor\(project\.path, editorId\)/)
  assert.match(workspace, /MoveNativeEditor\(sessionId, x, y, width, height\)/)
  assert.match(workspace, /disabled=\{!usable\}/)
  assert.match(workspace, /is not installed on this computer/)
  assert.match(workspace, /label="Code editors"[\s\S]*editorIntegrations[\s\S]*label="Tools"[\s\S]*Browser/)
})

test("App View keeps configuration, lifecycle, preview, and automation", async () => {
  const view = await source("features/projects/AppView.tsx")
  for (const contract of [
    "Configure App",
    "onStart",
    "onStop",
    "onRestart",
    "onSmoke",
    "onCapture",
    "normalizeAppPreviewURL",
  ]) assert.match(view, new RegExp(contract))
  assert.match(view, /className="flex min-h-0 flex-1 flex-col overflow-hidden/)
  assert.match(view, /relative mx-auto min-h-\[300px\][\s\S]*className="absolute inset-0 size-full border-0"/)
})

test("unconfigured App shows the empty state and hides the technical configuration", async () => {
  const view = await source("features/projects/AppView.tsx")
  for (const text of [
    "No app mounted here yet",
    "Ask your AI",
    "Open terminal",
    "Manual configuration",
    "Seizen will show the preview once it detects a port",
  ]) assert.match(view, new RegExp(text))
  assert.doesNotMatch(view, /New App/)
  assert.match(view, /apps\.length === 0 && !editor/)
  assert.match(view, /apps\.length > 0 && \(/)
})

test("AI flow confirms the task, uses real events, and allows canceling", async () => {
  const [view, service] = await Promise.all([
    source("features/projects/AppView.tsx"),
    source("features/projects/project-service.ts"),
  ])
  for (const contract of [
    "listProjectAgentSessions",
    "Choose an AI session",
    "Open Claude Code",
    "Open Codex",
    "Confirm the task for the AI",
    "app.discovery.started",
    "app.configuration.completed",
    "app.preview.ready",
    "Last MCP tool",
    "cancelAgentWork",
  ]) assert.match(view, new RegExp(contract))
  assert.match(service, /CancelAgentTask/)
  assert.doesNotMatch(view, /progressPercent|progress percentage/i)
})

test("managed terminal, multiple candidates, and primary App stay reachable", async () => {
  const [view, workspace, service] = await Promise.all([
    source("features/projects/AppView.tsx"),
    source("features/projects/ProjectWorkspace.tsx"),
    source("features/projects/project-service.ts"),
  ])
  for (const contract of [
    "Detect now",
    "Link to Seizen",
    "CandidatePicker",
    "canMountCandidate",
    "Mark as Main",
    "app.primary.updated",
  ]) assert.match(view, new RegExp(contract))
  assert.match(service, /SetPrimaryApp/)
  assert.match(workspace, /sessionNodesRef\.current\.get\(sessionId\)/)
  assert.match(workspace, /\{projectMode === "app" && \([\s\S]*<AppView/)
})

test("Server Lab links App, diagram, working terminal, and logs", async () => {
  const [view, diagram] = await Promise.all([
    source("features/projects/ServerLabView.tsx"),
    source("features/servers/ServerDiagram.tsx"),
  ])
  for (const contract of [
    "First set up an App",
    "Diagram",
    "Root terminal",
    "ServerTerminalPanel",
    "getServerLogs",
  ]) assert.match(view, new RegExp(contract))
  assert.match(diagram, /Service terminal/)
})

test("global servers offer start, stop, and open", async () => {
  const view = await source("features/servers/ServersView.tsx")
  for (const contract of ["Start", "Stop", "onOpen(server)", "listAllServers"]) {
    assert.match(view, new RegExp(contract.replace(/[()]/g, "\\$&")))
  }
})

test("Library uses a workspace thumbnail when there is no image", async () => {
  const library = await source("features/projects/ProjectLibrary.tsx")
  assert.match(
    library,
    /\)\s*:\s*\(\s*<span\s+data-project-thumbnail="workspace"/,
  )
  assert.match(library, /radial-gradient\(circle, var\(--dot\)/)
  assert.match(library, /&gt;_/)
})

test("Tools adds managed photos as persistent panels", async () => {
  const [workspace, model, service] = await Promise.all([
    source("features/projects/ProjectWorkspace.tsx"),
    source("features/projects/workspace-model.ts"),
    source("features/projects/project-service.ts"),
  ])
  assert.match(workspace, /label="Tools"[\s\S]*Add photo/)
  assert.match(workspace, /chooseProjectWorkspacePhoto\(project\)/)
  assert.match(workspace, /type: "photo"[\s\S]*assetId: asset\.assetId/)
  assert.match(workspace, /<WorkspacePhotoPanel/)
  assert.match(workspace, /draggable=\{false\}/)
  assert.match(workspace, /select-none object-contain/)
  assert.match(model, /type StoredPhotoNode[\s\S]*assetId: string/)
  assert.match(model, /node\.type === "photo"[\s\S]*assetId: node\.assetId/)
  for (const method of [
    "ChooseProjectWorkspacePhoto",
    "GetProjectWorkspacePhoto",
    "DeleteProjectWorkspacePhoto",
  ]) assert.match(service, new RegExp(method))
})
