package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Experiment struct {
	ID                string  `json:"id"`
	ProjectID         string  `json:"projectId"`
	Kind              string  `json:"kind"`
	AppID             string  `json:"appId"`
	BaseServerID      *string `json:"baseServerId"`
	Name              string  `json:"name"`
	Objective         string  `json:"objective"`
	BaseBranch        string  `json:"baseBranch"`
	BranchName        string  `json:"branchName"`
	BaseCommit        string  `json:"baseCommit"`
	WorktreePath      string  `json:"worktreePath"`
	Status            string  `json:"status"`
	CreatedBy         string  `json:"createdBy"`
	AgentSessionID    *string `json:"agentSessionId"`
	RiskLevel         string  `json:"riskLevel"`
	RiskReasonsJSON   string  `json:"riskReasonsJson"`
	ConfigurationJSON string  `json:"configurationJson"`
	CreatedAt         string  `json:"createdAt"`
	UpdatedAt         string  `json:"updatedAt"`
	ReviewReadyAt     *string `json:"reviewReadyAt"`
	IntegratedAt      *string `json:"integratedAt"`
	DiscardedAt       *string `json:"discardedAt"`
}

type ProjectContext struct {
	ProjectID    string `json:"projectId"`
	ExperimentID string `json:"experimentId"`
	Name         string `json:"name"`
	Kind         string `json:"kind"`
	BranchName   string `json:"branchName"`
	Path         string `json:"path"`
	Status       string `json:"status"`
}

type experimentRecordInput struct {
	ID                string
	ProjectID         string
	Kind              string
	AppID             string
	BaseServerID      string
	Name              string
	Objective         string
	BaseBranch        string
	BranchName        string
	BaseCommit        string
	WorktreePath      string
	Status            string
	CreatedBy         string
	AgentSessionID    string
	RiskLevel         string
	RiskReasonsJSON   string
	ConfigurationJSON string
}

const experimentColumns = `id, project_id, kind, app_id, base_server_id, name, objective,
base_branch, branch_name, base_commit, worktree_path, status, created_by, agent_session_id,
risk_level, risk_reasons_json, configuration_json, created_at, updated_at, review_ready_at, integrated_at, discarded_at`

func (a *App) ListExperiments(projectID, kind string) ([]Experiment, error) {
	projectID, kind = strings.TrimSpace(projectID), strings.TrimSpace(kind)
	if projectID == "" {
		return nil, errors.New("the project is required")
	}
	if kind != "" && kind != "app" && kind != "server" {
		return nil, errors.New("the experiment type is not valid")
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return nil, err
	}
	rows, err := db.Query(`SELECT `+experimentColumns+` FROM experiments
WHERE project_id = ? AND (? = '' OR kind = ?) ORDER BY updated_at DESC, LOWER(name)`, projectID, kind, kind)
	if err != nil {
		return nil, fmt.Errorf("could not load experiments: %w", err)
	}
	defer rows.Close()
	items := make([]Experiment, 0)
	for rows.Next() {
		item, scanErr := scanExperiment(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (a *App) GetProjectContext(projectID string) (ProjectContext, error) {
	return loadProjectContext(a.context(), a.database, projectID)
}

func (a *App) LinkExperimentAgentSession(experimentID, sessionID string) error {
	manager := a.currentTerminalManager()
	if manager == nil {
		return errors.New("the agent session was not found")
	}
	session := manager.session(strings.TrimSpace(sessionID))
	if session == nil || session.agent == "" || session.experimentID != strings.TrimSpace(experimentID) {
		return errors.New("the session does not belong to the experiment")
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return err
	}
	result, err := db.ExecContext(a.context(), `UPDATE experiments SET agent_session_id = ?, updated_at = `+projectNow+`
WHERE id = ? AND project_id = ?`, sessionID, experimentID, session.projectID)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed != 1 {
		return errors.New("the experiment was not found")
	}
	return nil
}

func (a *App) SelectProjectExperiment(projectID, experimentID string) (ProjectContext, error) {
	projectID, experimentID = strings.TrimSpace(projectID), strings.TrimSpace(experimentID)
	if projectID == "" {
		return ProjectContext{}, errors.New("the project is required")
	}
	db, err := a.database.Pool(a.context())
	if err != nil {
		return ProjectContext{}, err
	}
	if experimentID != "" {
		experiment, loadErr := scanExperiment(db.QueryRow(`SELECT `+experimentColumns+` FROM experiments WHERE id = ? AND project_id = ?`, experimentID, projectID))
		err = loadErr
		if errors.Is(err, sql.ErrNoRows) {
			return ProjectContext{}, errors.New("the experiment does not belong to the project")
		}
		if err != nil {
			return ProjectContext{}, err
		}
		if experiment.Status == "discarded" || experiment.Status == "archived" {
			return ProjectContext{}, errors.New("restore the experiment before selecting it")
		}
		if _, err = validateExperimentWorktree(experiment); err != nil {
			return ProjectContext{}, err
		}
	}
	_, err = db.Exec(`INSERT INTO project_contexts (project_id, experiment_id, updated_at)
VALUES (?, NULLIF(?, ''), `+projectNow+`)
ON CONFLICT (project_id) DO UPDATE SET experiment_id = excluded.experiment_id, updated_at = excluded.updated_at`, projectID, experimentID)
	if err != nil {
		return ProjectContext{}, fmt.Errorf("could not save the project context: %w", err)
	}
	context, err := loadProjectContext(a.context(), a.database, projectID)
	if err == nil {
		a.emitAgentEvent("experiment.selected", context)
	}
	return context, err
}

func projectPathForExperiment(ctx context.Context, db *sql.DB, projectID, experimentID string) (string, error) {
	if strings.TrimSpace(experimentID) == "" {
		var path string
		if err := db.QueryRowContext(ctx, `SELECT path FROM projects WHERE id = ?`, projectID).Scan(&path); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return "", errors.New("the project was not found")
			}
			return "", err
		}
		return existingDirectory(path)
	}
	experiment, err := scanExperiment(db.QueryRowContext(ctx, `SELECT `+experimentColumns+` FROM experiments WHERE id = ? AND project_id = ?`, experimentID, projectID))
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("the experiment does not belong to the project")
	}
	if err != nil {
		return "", err
	}
	_, err = validateExperimentWorktree(experiment)
	if err != nil {
		return "", err
	}
	return existingDirectory(experiment.WorktreePath)
}

func loadProjectContext(ctx context.Context, database *Database, projectID string) (ProjectContext, error) {
	projectID = strings.TrimSpace(projectID)
	if projectID == "" {
		return ProjectContext{}, errors.New("the project is required")
	}
	db, err := database.Pool(ctx)
	if err != nil {
		return ProjectContext{}, err
	}
	var result ProjectContext
	var experimentID, name, kind, mainBranch, experimentBranch, worktree, status sql.NullString
	err = db.QueryRowContext(ctx, `SELECT projects.id, projects.path, projects.branch,
project_contexts.experiment_id, experiments.name, experiments.kind, experiments.branch_name,
experiments.worktree_path, experiments.status
FROM projects
LEFT JOIN project_contexts ON project_contexts.project_id = projects.id
LEFT JOIN experiments ON experiments.id = project_contexts.experiment_id AND experiments.project_id = projects.id
WHERE projects.id = ?`, projectID).Scan(&result.ProjectID, &result.Path, &mainBranch,
		&experimentID, &name, &kind, &experimentBranch, &worktree, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return ProjectContext{}, errors.New("the project was not found")
	}
	if err != nil {
		return ProjectContext{}, fmt.Errorf("could not load the project context: %w", err)
	}
	if experimentID.Valid {
		result.ExperimentID = experimentID.String
		result.Name = name.String
		result.Kind = kind.String
		result.BranchName = experimentBranch.String
		result.Path = worktree.String
		result.Status = status.String
		return result, nil
	}
	result.Name = "Principal"
	result.Kind = "main"
	result.BranchName = mainBranch.String
	result.Status = "active"
	return result, nil
}

func createExperimentRecord(ctx context.Context, db *sql.DB, input experimentRecordInput) (Experiment, error) {
	input.ProjectID, input.Kind, input.AppID = strings.TrimSpace(input.ProjectID), strings.TrimSpace(input.Kind), strings.TrimSpace(input.AppID)
	input.Name, input.Objective = strings.TrimSpace(input.Name), strings.TrimSpace(input.Objective)
	if input.ProjectID == "" || input.AppID == "" || input.Name == "" {
		return Experiment{}, errors.New("the project, the App, and the name are required")
	}
	if input.Kind != "app" && input.Kind != "server" {
		return Experiment{}, errors.New("the experiment type is not valid")
	}
	if input.Kind == "server" && strings.TrimSpace(input.BaseServerID) == "" {
		return Experiment{}, errors.New("the server experiment requires a base server")
	}
	if input.CreatedBy == "" {
		input.CreatedBy = "user"
	}
	if input.CreatedBy != "user" && input.CreatedBy != "agent" {
		return Experiment{}, errors.New("the experiment creator is not valid")
	}
	if input.Status == "" {
		input.Status = "draft"
	}
	if input.RiskLevel == "" {
		input.RiskLevel = "low"
	}
	if input.RiskReasonsJSON == "" {
		input.RiskReasonsJSON = "[]"
	}
	if input.ConfigurationJSON == "" {
		input.ConfigurationJSON = "{}"
	}
	if !json.Valid([]byte(input.RiskReasonsJSON)) || !json.Valid([]byte(input.ConfigurationJSON)) {
		return Experiment{}, errors.New("the configuration and risk reasons must be valid JSON")
	}
	if input.ID == "" {
		var err error
		input.ID, err = newUUID()
		if err != nil {
			return Experiment{}, err
		}
	}
	var owned int
	if err := db.QueryRowContext(ctx, `SELECT 1 FROM apps WHERE id = ? AND project_id = ?`, input.AppID, input.ProjectID).Scan(&owned); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Experiment{}, errors.New("the experiment requires an App from the project")
		}
		return Experiment{}, err
	}
	if input.BaseServerID != "" {
		if err := db.QueryRowContext(ctx, `SELECT 1 FROM servers WHERE id = ? AND project_id = ? AND app_id = ? AND experiment_id IS NULL`, input.BaseServerID, input.ProjectID, input.AppID).Scan(&owned); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return Experiment{}, errors.New("the base server does not belong to the main App")
			}
			return Experiment{}, err
		}
	}
	item, err := scanExperiment(db.QueryRowContext(ctx, `INSERT INTO experiments (
id, project_id, kind, app_id, base_server_id, name, objective, base_branch, branch_name,
base_commit, worktree_path, status, created_by, agent_session_id, risk_level,
risk_reasons_json, configuration_json, created_at, updated_at) VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, ?, ?, ?, ?, ?, NULLIF(?, ''), ?, ?, ?, `+projectNow+`, `+projectNow+`)
RETURNING `+experimentColumns, input.ID, input.ProjectID, input.Kind, input.AppID,
		input.BaseServerID, input.Name, input.Objective, input.BaseBranch, input.BranchName,
		input.BaseCommit, input.WorktreePath, input.Status, input.CreatedBy, input.AgentSessionID,
		input.RiskLevel, input.RiskReasonsJSON, input.ConfigurationJSON))
	if err != nil {
		return Experiment{}, fmt.Errorf("could not save the experiment: %w", err)
	}
	return item, nil
}

func scanExperiment(row rowScanner) (Experiment, error) {
	var item Experiment
	err := row.Scan(&item.ID, &item.ProjectID, &item.Kind, &item.AppID, &item.BaseServerID,
		&item.Name, &item.Objective, &item.BaseBranch, &item.BranchName, &item.BaseCommit,
		&item.WorktreePath, &item.Status, &item.CreatedBy, &item.AgentSessionID, &item.RiskLevel,
		&item.RiskReasonsJSON, &item.ConfigurationJSON, &item.CreatedAt, &item.UpdatedAt, &item.ReviewReadyAt,
		&item.IntegratedAt, &item.DiscardedAt)
	return item, err
}
