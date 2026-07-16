<div align="center">

<img src="build/appicon.png" alt="Seizen" height="120" />

# Seizen

### Your projects, your agents, your tools — one canvas. A desktop workspace where AI terminals, code editors, and browsers live together as movable panels.

![Platform](https://img.shields.io/badge/platform-Windows%20x64-1f6feb?style=flat-square)
![Built with](https://img.shields.io/badge/built%20with-Wails%20·%20Go%20·%20React-444?style=flat-square)
![Status](https://img.shields.io/badge/status-pre--release-orange?style=flat-square)

</div>

<div align="center">
  <img src=".github/media/demo-workspace-agents.gif" alt="Filling a workspace with Claude Code, Codex, and VS Code" width="760" />
</div>

---

## What is it?

Seizen is a desktop app that turns every project into a **workspace canvas**. Right-click the canvas and drop in what that project needs: Claude Code, Codex, or OpenCode running in real WSL terminals, VS Code or Zed embedded as panels, a browser, your music. Everything stays where you left it — per project.

No window juggling. Open a project and its whole working environment comes back with it.

---

## Features

Everything below is captured from the real, compiled app — no mockups.

### One canvas per project
Enter a project and build its workspace: AI agents, editors, and terminals as panels you drag, resize, and arrange like windows on a desk.

<div align="center"><img src=".github/media/demo-workspace-agents.gif" alt="Adding Claude Code, Codex, and VS Code to a workspace" width="720" /></div>

### Switch projects, keep everything
Each project remembers its own panels. Jump from one to another and the whole environment swaps with it — here, one project runs Claude, Codex, and VS Code while the other keeps Zed and Spotify.

<div align="center"><img src=".github/media/demo-switch-projects.gif" alt="Switching between projects" width="720" /></div>

### Agents, editors, and environments in one place
Manage where each AI agent runs (per-agent WSL distribution or Windows), whether it can skip approvals, and which editors and WSL environments Seizen installs and manages for you.

<div align="center"><img src=".github/media/demo-resources.gif" alt="Resources: AI agents, editors, and WSL environments" width="720" /></div>

---

## How it's built

- **[Wails](https://wails.io)** (Go) — native Windows shell; React renders in the system WebView, no browser bundled
- **Real agent terminals** — Claude Code, Codex, and OpenCode run in managed WSL 2 distributions (or Windows CMD) with per-project profiles and an MCP bridge into Seizen's tools
- **Native editor embedding** — Zed and other native editors are re-parented into the canvas via Win32 and pinned to their panel
- **Local-first** — a single SQLite database in `%APPDATA%\Seizen`; projects stay plain folders on disk

---

## Development

Requirements: Go 1.25+, Node.js, and Wails 2.13.

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0
wails dev
```

To build the Windows executable:

```powershell
wails build -clean
```

The result lands in `build/bin/Seizen.exe`.

### Repository layout

```
main.go          Wails entry point; embeds frontend/dist and calls core.Run
internal/core/   All application code (one Go package) and SQL migrations
frontend/        React + Vite UI
build/           Packaging assets (icon, installer, manifest)
skills/          Agent skills shipped with the app
infra/           Coder-on-Incus workspace template (optional)
```

## License

Seizen is licensed under [CC BY-NC-SA 4.0](https://creativecommons.org/licenses/by-nc-sa/4.0/) (Attribution-NonCommercial-ShareAlike).

- **Attribution** — you must give credit to the original author ([FaridDevU](https://github.com/FaridDevU)).
- **NonCommercial** — you may not use this project for commercial purposes.
- **ShareAlike** — if you remix or build upon it, you must distribute your contributions under the same license.

See [LICENSE](LICENSE) for the full text. For commercial licensing, contact the author.
