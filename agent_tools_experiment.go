package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type agentExperimentAnalyzeInput struct {
	Description  string   `json:"description" jsonschema:"Summary of the planned change."`
	Areas        []string `json:"areas,omitempty" jsonschema:"Expected files, modules, Apps, or infrastructure areas."`
	ChangeType   string   `json:"changeType,omitempty" jsonschema:"Category such as feature, redesign, migration, infrastructure, or destructive."`
	Migrations   []string `json:"migrations,omitempty" jsonschema:"Planned database or data migrations."`
	Dependencies []string `json:"dependencies,omitempty" jsonschema:"Dependencies to add or upgrade."`
	Risks        []string `json:"risks,omitempty" jsonschema:"Known risks and irreversible effects."`
	Context      string   `json:"context,omitempty" jsonschema:"Current task and Seizen context."`
	FileCount    int      `json:"fileCount,omitempty" jsonschema:"Estimated number of files."`
}

type agentExperimentCreateInput struct {
	RequestID     string `json:"requestId" jsonschema:"Identifier returned by seizen_experiment_suggest."`
	ApprovalID    string `json:"approvalId" jsonschema:"Recent user approval identifier."`
	Name          string `json:"name" jsonschema:"Human-readable experiment name."`
	Objective     string `json:"objective" jsonschema:"Goal preserved in the handoff."`
	Kind          string `json:"kind" jsonschema:"app or server."`
	AppID         string `json:"appId" jsonschema:"Authorized App identifier."`
	BaseServerID  string `json:"baseServerId,omitempty" jsonschema:"Required principal server for server experiments."`
	BranchName    string `json:"branchName,omitempty" jsonschema:"Optional safe branch under experiment/."`
	DecisionsJSON string `json:"decisionsJson,omitempty" jsonschema:"JSON array of decisions to preserve in the handoff."`
}

type agentExperimentIDInput struct {
	ExperimentID string `json:"experimentId" jsonschema:"Experiment identifier in the authorized project."`
}

type agentExperimentApprovalInput struct {
	ExperimentID string `json:"experimentId" jsonschema:"Experiment identifier in the authorized project."`
	ApprovalID   string `json:"approvalId,omitempty" jsonschema:"Recent single-use approval; omit to request it."`
	BackupDirty  bool   `json:"backupDirty,omitempty" jsonschema:"Create a checkpoint before discard if needed."`
	DeleteBranch bool   `json:"deleteBranch,omitempty" jsonschema:"Delete the local experiment branch after cleanup."`
}

type ExperimentHandoff struct {
	ID              string `json:"id"`
	ExperimentID    string `json:"experimentId"`
	SourceSessionID string `json:"sourceSessionId"`
	TargetSessionID string `json:"targetSessionId"`
	Objective       string `json:"objective"`
	PlanJSON        string `json:"planJson"`
	DecisionsJSON   string `json:"decisionsJson"`
	CreatedAt       string `json:"createdAt"`
}

func addAgentExperimentTools(server *mcp.Server, client *agentRPCClient) {
	addAgentTool(server, client, "seizen_experiment_analyze_change", "Analyze semantic change risk without creating or modifying anything.", agentExperimentAnalyzeInput{})
	addAgentTool(server, client, "seizen_experiment_suggest", "Create one visible user decision request for the analyzed plan.", agentExperimentAnalyzeInput{})
	addAgentTool(server, client, "seizen_experiment_create", "Create an approved branch, worktree, experiment, and safe agent handoff.", agentExperimentCreateInput{})
	addAgentTool(server, client, "seizen_experiment_list", "List experiments in the authorized project.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_experiment_select", "Select Principal or one experiment for the shared project UI context.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_status", "Read one authorized experiment and its Git metadata.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_checkpoint", "Create a safe Git checkpoint inside the authorized experiment worktree.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_compare", "Compare the experiment against its base without changing Principal.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_prepare_integration", "Run review and tests in a temporary integration worktree without changing Principal.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_request_integration", "Create a visible user approval request for a verified integration.", agentExperimentIDInput{})
	addAgentTool(server, client, "seizen_experiment_integrate", "Integrate only with a recent single-use user approval.", agentExperimentApprovalInput{})
	addAgentTool(server, client, "seizen_experiment_discard", "Request confirmation or safely discard runtimes and the worktree.", agentExperimentApprovalInput{})
	addAgentTool(server, client, "seizen_experiment_archive", "Request confirmation or archive resources while retaining branch metadata.", agentExperimentApprovalInput{})
	addAgentTool(server, client, "seizen_experiment_restore", "Request confirmation and reconstruct an archived experiment from its retained branch and declarative metadata.", agentExperimentApprovalInput{})
}

func (bridge *AgentBridge) callExperimentTool(ctx context.Context, scope AgentTokenScope, tool string, arguments json.RawMessage) (any, error) {
	switch tool {
	case "seizen_experiment_analyze_change", "seizen_experiment_suggest":
		var input agentExperimentAnalyzeInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		change := ExperimentChangeInput{
			ProjectID: scope.ProjectID, Description: input.Description, Areas: input.Areas,
			ChangeType: input.ChangeType, Migrations: input.Migrations, Dependencies: input.Dependencies,
			Risks: input.Risks, Context: input.Context, FileCount: input.FileCount,
		}
		if tool == "seizen_experiment_analyze_change" {
			return bridge.app.AnalyzeExperimentChange(change)
		}
		return bridge.app.suggestExperimentChange(scope, change)
	case "seizen_experiment_create":
		if scope.ExperimentID != "" {
			return nil, errors.New("cannot create another experiment from an experiment-scoped token")
		}
		var input agentExperimentCreateInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		return bridge.createApprovedExperiment(ctx, scope, input)
	case "seizen_experiment_list":
		return bridge.app.ListExperiments(scope.ProjectID, "")
	case "seizen_experiment_select", "seizen_experiment_status", "seizen_experiment_checkpoint", "seizen_experiment_compare", "seizen_experiment_prepare_integration", "seizen_experiment_request_integration":
		var input agentExperimentIDInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		if tool == "seizen_experiment_select" {
			if scope.ExperimentID != "" && input.ExperimentID != scope.ExperimentID {
				return nil, errors.New("the token cannot select another experiment")
			}
			return bridge.app.SelectProjectExperiment(scope.ProjectID, input.ExperimentID)
		}
		experiment, err := bridge.loadScopedExperiment(ctx, scope, input.ExperimentID)
		if err != nil {
			return nil, err
		}
		if tool == "seizen_experiment_checkpoint" {
			commit, checkpointErr := bridge.app.CreateExperimentCheckpoint(experiment.ID)
			return map[string]string{"commit": commit}, checkpointErr
		}
		if tool == "seizen_experiment_compare" {
			return bridge.app.CompareExperiment(experiment.ID)
		}
		if tool == "seizen_experiment_prepare_integration" {
			return bridge.app.PrepareExperimentIntegration(experiment.ID, true)
		}
		if tool == "seizen_experiment_request_integration" {
			return bridge.app.RequestExperimentIntegration(experiment.ID, true)
		}
		return experiment, nil
	case "seizen_experiment_integrate", "seizen_experiment_discard", "seizen_experiment_archive", "seizen_experiment_restore":
		var input agentExperimentApprovalInput
		if err := decodeAgentArguments(arguments, &input); err != nil {
			return nil, err
		}
		experiment, err := bridge.loadScopedExperiment(ctx, scope, input.ExperimentID)
		if err != nil {
			return nil, err
		}
		action := strings.TrimPrefix(tool, "seizen_")
		if tool == "seizen_experiment_integrate" {
			return bridge.app.IntegrateExperiment(experiment.ID, input.ApprovalID, true)
		}
		if input.ApprovalID == "" {
			return bridge.app.requestAgentApproval(scope, action, experiment.ID, input)
		}
		if err = bridge.app.consumeAgentApproval(scope, input.ApprovalID, action, experiment.ID); err != nil {
			return nil, err
		}
		if tool == "seizen_experiment_discard" {
			return bridge.app.DiscardExperiment(ExperimentCleanupInput{ExperimentID: experiment.ID, Confirmed: true, BackupDirty: input.BackupDirty, DeleteBranch: input.DeleteBranch})
		}
		if tool == "seizen_experiment_restore" {
			return bridge.app.RestoreExperiment(experiment.ID, true)
		}
		return bridge.app.ArchiveExperiment(experiment.ID, true)
	default:
		return nil, errors.New("unrecognized experiment tool")
	}
}

func (bridge *AgentBridge) createApprovedExperiment(ctx context.Context, scope AgentTokenScope, input agentExperimentCreateInput) (map[string]any, error) {
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return nil, err
	}
	var planHash, inputJSON, analysisJSON, approvalID string
	err = db.QueryRowContext(ctx, `SELECT plan_hash, input_json, analysis_json, COALESCE(approval_id, '')
FROM experiment_change_requests WHERE id = ? AND session_id = ? AND project_id = ?`,
		strings.TrimSpace(input.RequestID), scope.SessionID, scope.ProjectID).Scan(&planHash, &inputJSON, &analysisJSON, &approvalID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errors.New("the experiment request does not belong to this task")
	}
	if err != nil {
		return nil, err
	}
	if approvalID != strings.TrimSpace(input.ApprovalID) {
		return nil, errors.New("the approval does not match the request")
	}
	if err = bridge.app.consumeAgentApproval(scope, approvalID, "experiment.create", planHash); err != nil {
		return nil, err
	}
	var analysis ExperimentChangeAnalysis
	if err = json.Unmarshal([]byte(analysisJSON), &analysis); err != nil {
		return nil, err
	}
	reasons, _ := json.Marshal(analysis.Reasons)
	experiment, err := bridge.app.CreateExperiment(ExperimentCreateInput{
		ProjectID: scope.ProjectID, Kind: input.Kind, AppID: input.AppID, BaseServerID: input.BaseServerID,
		Name: input.Name, Objective: input.Objective, BranchName: input.BranchName,
		CreatedBy: "agent", AgentSessionID: scope.SessionID, RiskLevel: analysis.RiskLevel,
		RiskReasonsJSON: string(reasons), Confirmed: true,
	})
	if err != nil {
		return nil, err
	}
	handoff, err := bridge.app.handoffExperimentAgent(scope, experiment, inputJSON, input.DecisionsJSON)
	if err != nil {
		return map[string]any{"experiment": experiment}, fmt.Errorf("the experiment was created, but the handoff failed: %w", err)
	}
	return map[string]any{"experiment": experiment, "handoff": handoff}, nil
}

func (bridge *AgentBridge) loadScopedExperiment(ctx context.Context, scope AgentTokenScope, experimentID string) (Experiment, error) {
	experimentID = strings.TrimSpace(experimentID)
	if experimentID == "" {
		return Experiment{}, errors.New("the experiment is required")
	}
	if scope.ExperimentID != "" && scope.ExperimentID != experimentID {
		return Experiment{}, errors.New("the token is limited to another experiment")
	}
	db, err := bridge.app.database.Pool(ctx)
	if err != nil {
		return Experiment{}, err
	}
	experiment, err := scanExperiment(db.QueryRowContext(ctx, `SELECT `+experimentColumns+` FROM experiments WHERE id = ? AND project_id = ?`, experimentID, scope.ProjectID))
	if errors.Is(err, sql.ErrNoRows) {
		return Experiment{}, errors.New("the experiment does not belong to the authorized project")
	}
	return experiment, err
}

func (a *App) handoffExperimentAgent(scope AgentTokenScope, experiment Experiment, planJSON, decisionsJSON string) (ExperimentHandoff, error) {
	manager := a.currentTerminalManager()
	if manager == nil {
		return ExperimentHandoff{}, errors.New("the source session was not found")
	}
	source := manager.session(scope.SessionID)
	if source == nil || source.projectID != scope.ProjectID || source.agent == "" {
		return ExperimentHandoff{}, errors.New("the source session cannot be transferred")
	}
	startAgent := a.StartProjectAgentTerminalContext
	if a.startExperimentAgent != nil {
		startAgent = a.startExperimentAgent
	}
	targetID, err := startAgent(scope.ProjectID, experiment.ID, source.agent, experiment.AppID)
	if err != nil {
		return ExperimentHandoff{}, err
	}
	if decisionsJSON == "" {
		decisionsJSON = "[]"
	}
	if !json.Valid([]byte(decisionsJSON)) {
		_ = a.StopProjectTerminal(targetID)
		return ExperimentHandoff{}, errors.New("the handoff decisions are not valid JSON")
	}
	id, err := newUUID()
	if err != nil {
		_ = a.StopProjectTerminal(targetID)
		return ExperimentHandoff{}, err
	}
	db, err := a.database.Pool(context.Background())
	if err != nil {
		_ = a.StopProjectTerminal(targetID)
		return ExperimentHandoff{}, err
	}
	handoff := ExperimentHandoff{
		ID: id, ExperimentID: experiment.ID, SourceSessionID: scope.SessionID, TargetSessionID: targetID,
		Objective: experiment.Objective, PlanJSON: planJSON, DecisionsJSON: decisionsJSON,
	}
	err = db.QueryRow(`INSERT INTO experiment_handoffs
(id, experiment_id, source_session_id, target_session_id, objective, plan_json, decisions_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, `+projectNow+`) RETURNING created_at`, id, experiment.ID, scope.SessionID,
		targetID, experiment.Objective, planJSON, decisionsJSON).Scan(&handoff.CreatedAt)
	if err != nil {
		_ = a.StopProjectTerminal(targetID)
		return ExperimentHandoff{}, err
	}
	if err = a.LinkExperimentAgentSession(experiment.ID, targetID); err != nil {
		return ExperimentHandoff{}, err
	}
	prompt := fmt.Sprintf("Continue this task in experiment %q. Objective: %s\nPlan: %s\nDecisions: %s\nUse seizen_project_context before editing.", experiment.Name, experiment.Objective, planJSON, decisionsJSON)
	if err = a.SendAgentTaskContext(scope.ProjectID, experiment.ID, currentProjectSpaceID, targetID, prompt, true); err != nil {
		return ExperimentHandoff{}, err
	}
	a.emitAgentEvent("experiment.handoff.created", handoff)
	return handoff, nil
}
