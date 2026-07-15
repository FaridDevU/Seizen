package core

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strings"
)

type ExperimentChangeInput struct {
	ProjectID    string   `json:"projectId"`
	Description  string   `json:"description"`
	Areas        []string `json:"areas"`
	ChangeType   string   `json:"changeType"`
	Migrations   []string `json:"migrations"`
	Dependencies []string `json:"dependencies"`
	Risks        []string `json:"risks"`
	Context      string   `json:"context"`
	FileCount    int      `json:"fileCount"`
}

type ExperimentChangeAnalysis struct {
	PlanHash             string   `json:"planHash"`
	RiskLevel            string   `json:"riskLevel"`
	Reasons              []string `json:"reasons"`
	RecommendExperiment  bool     `json:"recommendExperiment"`
	AllowPrincipal       bool     `json:"allowPrincipal"`
	NeedsAdvancedConfirm bool     `json:"needsAdvancedConfirm"`
}

type ExperimentSuggestion struct {
	RequestID string                   `json:"requestId"`
	Analysis  ExperimentChangeAnalysis `json:"analysis"`
	Approval  AgentApproval            `json:"approval"`
	Repeated  bool                     `json:"repeated"`
	Decision  string                   `json:"decision"`
}

func (a *App) AnalyzeExperimentChange(input ExperimentChangeInput) (ExperimentChangeAnalysis, error) {
	analysis, err := analyzeExperimentChange(input)
	if err == nil && analysis.RiskLevel != "low" {
		a.emitAgentEvent("experiment.risk.detected", map[string]any{"projectId": input.ProjectID, "analysis": analysis})
	}
	return analysis, err
}

func analyzeExperimentChange(input ExperimentChangeInput) (ExperimentChangeAnalysis, error) {
	input.ProjectID, input.Description = strings.TrimSpace(input.ProjectID), strings.TrimSpace(input.Description)
	if input.ProjectID == "" || input.Description == "" {
		return ExperimentChangeAnalysis{}, errors.New("the project and the change description are required")
	}
	payload, _ := json.Marshal(input)
	hash := sha256.Sum256(payload)
	result := ExperimentChangeAnalysis{PlanHash: hex.EncodeToString(hash[:]), RiskLevel: "low", AllowPrincipal: true}
	text := strings.ToLower(strings.Join(append(append(append([]string{input.Description, input.ChangeType, input.Context}, input.Areas...), input.Migrations...), append(input.Dependencies, input.Risks...)...), " "))
	critical := riskMatches(text, []string{
		"destructive", "destructiv", "drop table", "drop column", "delete data", "delete data", "delete data",
		"truncate", "irreversible", "data loss", "data loss",
	})
	high := riskMatches(text, []string{
		"authentication", "authentication", "authentication", "authorization", "authorization", "authorization",
		"payment", "payment", "secret", "secret", "architecture", "architecture", "framework",
		"docker", "compose", "nginx", "redis", "worker", "network", "network ", "infrastructure", "infrastructure",
		"major dependency", "major dependency", "mass rename", "mass rename", "mass delete", "mass delete",
	})
	migration := len(input.Migrations) > 0 || riskMatches(text, []string{"migration", "migration", "migration", "schema", "database"})
	multiArea := input.FileCount >= 8 || len(input.Areas) >= 4 || riskMatches(text, []string{"multiple apps", "multiple apps", "many modules", "many modules"})
	switch {
	case critical:
		result.RiskLevel, result.RecommendExperiment = "critical", true
		result.AllowPrincipal, result.NeedsAdvancedConfirm = false, true
		result.Reasons = append(result.Reasons, "destructive or potentially irreversible change")
	case high || (migration && multiArea):
		result.RiskLevel, result.RecommendExperiment = "high", true
		result.Reasons = append(result.Reasons, "affects a sensitive area or infrastructure")
	case migration || multiArea || input.FileCount >= 4:
		result.RiskLevel, result.RecommendExperiment = "medium", true
		result.Reasons = append(result.Reasons, "broad change or one that includes migration")
	default:
		result.Reasons = []string{"local and reversible change"}
	}
	if input.FileCount > 0 && input.FileCount >= 8 {
		result.Reasons = append(result.Reasons, "modifies many files")
	}
	sort.Strings(result.Reasons)
	return result, nil
}

func riskMatches(text string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(text, candidate) {
			return true
		}
	}
	return false
}

func (a *App) suggestExperimentChange(scope AgentTokenScope, input ExperimentChangeInput) (ExperimentSuggestion, error) {
	input.ProjectID = scope.ProjectID
	analysis, err := a.AnalyzeExperimentChange(input)
	if err != nil {
		return ExperimentSuggestion{}, err
	}
	db, err := a.database.Pool(context.Background())
	if err != nil {
		return ExperimentSuggestion{}, err
	}
	var requestID, approvalID, analysisJSON, decision string
	err = db.QueryRow(`SELECT id, COALESCE(approval_id, ''), analysis_json, decision FROM experiment_change_requests
WHERE session_id = ? AND project_id = ? AND plan_hash = ?`, scope.SessionID, scope.ProjectID, analysis.PlanHash).
		Scan(&requestID, &approvalID, &analysisJSON, &decision)
	if err == nil {
		approval, approvalErr := loadAgentApproval(db, approvalID)
		return ExperimentSuggestion{RequestID: requestID, Analysis: analysis, Approval: approval, Repeated: true, Decision: decision}, approvalErr
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ExperimentSuggestion{}, err
	}
	requestID, err = newUUID()
	if err != nil {
		return ExperimentSuggestion{}, err
	}
	approval, err := a.requestAgentApproval(scope, "experiment.create", analysis.PlanHash, map[string]any{"plan": input, "analysis": analysis})
	if err != nil {
		return ExperimentSuggestion{}, err
	}
	inputJSON, _ := json.Marshal(input)
	analysisPayload, _ := json.Marshal(analysis)
	_, err = db.Exec(`INSERT INTO experiment_change_requests
(id, session_id, project_id, plan_hash, input_json, analysis_json, approval_id, created_at, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?, `+projectNow+`, `+projectNow+`)`, requestID, scope.SessionID, scope.ProjectID,
		analysis.PlanHash, inputJSON, analysisPayload, approval.ID)
	if err != nil {
		return ExperimentSuggestion{}, err
	}
	result := ExperimentSuggestion{RequestID: requestID, Analysis: analysis, Approval: approval, Decision: "pending"}
	a.emitAgentEvent("experiment.suggested", map[string]any{"projectId": scope.ProjectID, "request": result})
	return result, nil
}

func (a *App) ContinueExperimentChangeOnPrincipal(approvalID string, advancedConfirmed bool) (AgentApproval, error) {
	db, err := a.database.Pool(a.context())
	if err != nil {
		return AgentApproval{}, err
	}
	var analysisJSON string
	if err = db.QueryRowContext(a.context(), `SELECT analysis_json FROM experiment_change_requests WHERE approval_id = ? AND decision = 'pending'`, approvalID).Scan(&analysisJSON); err != nil {
		return AgentApproval{}, errors.New("the request is no longer pending")
	}
	var analysis ExperimentChangeAnalysis
	if json.Unmarshal([]byte(analysisJSON), &analysis) != nil {
		return AgentApproval{}, errors.New("the saved analysis is not valid")
	}
	if analysis.NeedsAdvancedConfirm && !advancedConfirmed {
		return AgentApproval{}, errors.New("the critical change requires advanced confirmation to continue on Principal")
	}
	item, err := a.ResolveAgentApproval(approvalID, false)
	if err != nil {
		return AgentApproval{}, err
	}
	_, err = db.ExecContext(a.context(), `UPDATE experiment_change_requests SET decision = 'principal', updated_at = `+projectNow+` WHERE approval_id = ?`, approvalID)
	if err == nil {
		a.emitAgentEvent("experiment.rejected", map[string]any{"approval": item, "decision": "principal"})
	}
	return item, err
}

func loadAgentApproval(db *sql.DB, id string) (AgentApproval, error) {
	return scanAgentApproval(db.QueryRow(`SELECT id, session_id, project_id, COALESCE(experiment_id, ''), COALESCE(app_id, ''),
action, resource_id, request_json, status, expires_at, decided_at, consumed_at, created_at FROM agent_approvals WHERE id = ?`, id))
}
