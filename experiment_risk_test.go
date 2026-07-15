package main

import "testing"

func TestExperimentRiskAnalysisSmallImportantAndCritical(t *testing.T) {
	projectID := "project"
	small, err := analyzeExperimentChange(ExperimentChangeInput{ProjectID: projectID, Description: "Fix a typo", FileCount: 1})
	if err != nil || small.RiskLevel != "low" || small.RecommendExperiment {
		t.Fatalf("small = %#v, %v", small, err)
	}
	important, err := analyzeExperimentChange(ExperimentChangeInput{
		ProjectID: projectID, Description: "Change authentication and Redis", Areas: []string{"web", "api"}, FileCount: 9,
	})
	if err != nil || important.RiskLevel != "high" || !important.RecommendExperiment || !important.AllowPrincipal {
		t.Fatalf("important = %#v, %v", important, err)
	}
	critical, err := analyzeExperimentChange(ExperimentChangeInput{
		ProjectID: projectID, Description: "Destructive migration: DROP TABLE and delete data",
	})
	if err != nil || critical.RiskLevel != "critical" || critical.AllowPrincipal || !critical.NeedsAdvancedConfirm {
		t.Fatalf("critical = %#v, %v", critical, err)
	}
}

func TestExperimentSuggestionIsNotRepeatedAndStoresDecision(t *testing.T) {
	app, project := newAppServerTestApp(t)
	scope := AgentTokenScope{SessionID: "risk-agent", ProjectID: project.ID, SpaceID: currentProjectSpaceID}
	input := ExperimentChangeInput{Description: "Redesign authentication", FileCount: 12}
	first, err := app.suggestExperimentChange(scope, input)
	if err != nil || first.Repeated || first.Decision != "pending" || first.Approval.Status != "pending" {
		t.Fatalf("first = %#v, %v", first, err)
	}
	second, err := app.suggestExperimentChange(scope, input)
	if err != nil || !second.Repeated || second.RequestID != first.RequestID || second.Approval.ID != first.Approval.ID {
		t.Fatalf("second = %#v, %v", second, err)
	}
	if _, err = app.ContinueExperimentChangeOnPrincipal(first.Approval.ID, false); err != nil {
		t.Fatal(err)
	}
	third, err := app.suggestExperimentChange(scope, input)
	if err != nil || third.Decision != "principal" || third.Approval.Status != "denied" {
		t.Fatalf("third = %#v, %v", third, err)
	}
}

func TestCriticalSuggestionBlocksPrincipalWithoutAdvancedConfirmation(t *testing.T) {
	app, project := newAppServerTestApp(t)
	scope := AgentTokenScope{SessionID: "critical-agent", ProjectID: project.ID, SpaceID: currentProjectSpaceID}
	suggestion, err := app.suggestExperimentChange(scope, ExperimentChangeInput{Description: "DROP TABLE and delete data"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = app.ContinueExperimentChangeOnPrincipal(suggestion.Approval.ID, false); err == nil {
		t.Fatal("critical change continued without advanced confirmation")
	}
	if _, err = app.ContinueExperimentChangeOnPrincipal(suggestion.Approval.ID, true); err != nil {
		t.Fatal(err)
	}
}
