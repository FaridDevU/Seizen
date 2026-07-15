---
name: seizen-experiments
description: Detect important or risky changes in a Seizen project before editing and route them through Principal or an isolated experiment. Use for broad redesigns, authentication, authorization, payments, database migrations, destructive data work, architecture or framework changes, major dependency upgrades, Docker/Compose/Nginx/Redis/workers/network/secrets, infrastructure, mass renames or deletions, multi-App work, or changes spanning many modules.
---

# Seizen Experiment Guard

Before an important change:

1. Summarize the objective and plan, including expected files or areas, change type, migrations, dependencies, risks, and current Seizen context.
2. Call `seizen_experiment_analyze_change` with that structured plan before editing.
3. If the response recommends an experiment, call `seizen_experiment_suggest` and wait for the user's decision.
4. Do not create an experiment or continue a risky change on Principal until the user explicitly chooses.
5. For critical changes, do not continue on Principal without the advanced approval returned by Seizen.

Reuse the same plan fingerprint for follow-up analysis. Do not create repeated suggestions for the same task.

After approval, call `seizen_experiment_create`. Continue only in the returned worktree and new agent session. Obtain the current path and identifiers from `seizen_project_context`; never redirect an existing PTY silently.

While inside an experiment:

- Restrict files, commands, Apps, servers, previews, logs, and tokens to its `experimentId` and worktree.
- Use `seizen_experiment_checkpoint` for safe milestones.
- Use `seizen_experiment_compare` before declaring completion.
- Use `seizen_experiment_prepare_integration` for review; do not modify Principal.
- Request integration explicitly and call `seizen_experiment_integrate` only with a recent approval.
- Never force-push, delete a branch without confirmation, or remove a dirty worktree.
- For server work, export and rebuild reproducible configuration before integration; for WSL include a `seizen-rebuild.sh` that applies the tracked files inside the clean verification server. Never integrate live state, secrets, databases, volumes, sockets, memory, processes, or logs.

On discard or archive, use the corresponding Seizen tool so runtimes, terminals, servers, tokens, and worktrees are handled safely.
