package core

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const (
	agentBridgeURLEnv   = "SEIZEN_BRIDGE_URL"
	agentBridgeTokenEnv = "SEIZEN_BRIDGE_TOKEN"
)

type agentEmptyInput struct{}

type agentAppConfiguration struct {
	Name             string   `json:"name" jsonschema:"Human-readable App name."`
	Kind             string   `json:"kind" jsonschema:"App type: web or desktop."`
	WorkingDirectory string   `json:"workingDirectory,omitempty" jsonschema:"Project-relative or absolute working directory inside the project."`
	StartCommand     string   `json:"startCommand,omitempty" jsonschema:"Command used to start the App."`
	StopCommand      string   `json:"stopCommand,omitempty" jsonschema:"Optional graceful stop command."`
	TestCommand      string   `json:"testCommand,omitempty" jsonschema:"Optional test command."`
	Executable       string   `json:"executable,omitempty" jsonschema:"Desktop executable, relative to the working directory or absolute."`
	Arguments        []string `json:"arguments,omitempty" jsonschema:"Desktop executable arguments."`
	PreviewURL       string   `json:"previewUrl,omitempty" jsonschema:"Validated HTTP or HTTPS preview URL."`
	HealthcheckURL   string   `json:"healthcheckUrl,omitempty" jsonschema:"Validated HTTP or HTTPS healthcheck URL."`
}

type agentAppCreateInput struct {
	Name string `json:"name" jsonschema:"Human-readable App name."`
	Kind string `json:"kind" jsonschema:"App type: web or desktop."`
}

type agentAppConfigureInput struct {
	AppID         string                `json:"appId" jsonschema:"App identifier from seizen_app_list or seizen_app_create."`
	Configuration agentAppConfiguration `json:"configuration" jsonschema:"Complete replacement configuration."`
}

type agentAppIDInput struct {
	AppID string `json:"appId" jsonschema:"App identifier in the authorized project."`
}

type agentSetPreviewInput struct {
	AppID      string `json:"appId" jsonschema:"App identifier in the authorized project."`
	PreviewURL string `json:"previewUrl" jsonschema:"Full HTTP or HTTPS preview URL without credentials."`
}

type agentAppTestRouteInput struct {
	AppID string `json:"appId" jsonschema:"App identifier in the authorized project."`
	Route string `json:"route" jsonschema:"Route path relative to the preview URL, for example /login."`
}

func (input agentAppConfigureInput) appInput(projectID string) AppInput {
	return input.Configuration.appInput(projectID)
}

func (configuration agentAppConfiguration) appInput(projectID string) AppInput {
	arguments, _ := json.Marshal(configuration.Arguments)
	return AppInput{
		ProjectID: projectID, Name: configuration.Name, Kind: configuration.Kind,
		WorkingDirectory: configuration.WorkingDirectory, StartCommand: configuration.StartCommand,
		StopCommand: configuration.StopCommand, TestCommand: configuration.TestCommand,
		Executable: configuration.Executable, ArgumentsJSON: string(arguments),
		PreviewURL: configuration.PreviewURL, HealthcheckURL: configuration.HealthcheckURL,
	}
}

type agentRPCClient struct {
	endpoint string
	token    string
	client   *http.Client
}

func newAgentRPCClientFromEnvironment() (*agentRPCClient, error) {
	rawURL := strings.TrimSpace(os.Getenv(agentBridgeURLEnv))
	token := strings.TrimSpace(os.Getenv(agentBridgeTokenEnv))
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme != "http" || parsed.Hostname() != "127.0.0.1" || parsed.Port() == "" || parsed.User != nil {
		return nil, errors.New("Seizen's local bridge is not configured")
	}
	if token == "" {
		return nil, errors.New("Seizen's temporary token is not configured")
	}
	return &agentRPCClient{
		endpoint: strings.TrimRight(rawURL, "/") + "/agent/tool",
		token:    token,
		client:   &http.Client{},
	}, nil
}

func (client *agentRPCClient) Call(ctx context.Context, tool string, input any) (any, error) {
	payload, err := json.Marshal(agentRPCRequest{Tool: tool, Arguments: mustAgentJSON(input)})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, client.endpoint, bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("Seizen is not available: %w", err)
	}
	defer response.Body.Close()
	var output struct {
		Result json.RawMessage `json:"result"`
		Error  string          `json:"error"`
	}
	decoder := json.NewDecoder(response.Body)
	if err = decoder.Decode(&output); err != nil {
		return nil, errors.New("Seizen returned an invalid response")
	}
	if output.Error != "" {
		return nil, errors.New(output.Error)
	}
	if len(output.Result) == 0 {
		return map[string]any{}, nil
	}
	var result any
	decoder = json.NewDecoder(bytes.NewReader(output.Result))
	decoder.UseNumber()
	if err = decoder.Decode(&result); err != nil {
		return nil, errors.New("Seizen returned an invalid result")
	}
	return result, nil
}

func mustAgentJSON(input any) json.RawMessage {
	payload, err := json.Marshal(input)
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return payload
}

func runAgentMCPBridge(ctx context.Context) error {
	client, err := newAgentRPCClientFromEnvironment()
	if err != nil {
		return err
	}
	return newAgentMCPServer(client).Run(ctx, &mcp.StdioTransport{})
}

func newAgentMCPServer(client *agentRPCClient) *mcp.Server {
	server := mcp.NewServer(&mcp.Implementation{Name: "seizen-agent-bridge", Version: "1.0.0"}, nil)
	addAgentTool(server, client, "seizen_project_context", "Return the current Seizen project and selected App scope.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_app_list", "List Apps visible in the current Seizen project scope.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_app_create", "Create an unconfigured App draft in the current project, then bind this session to it.", agentAppCreateInput{})
	addAgentTool(server, client, "seizen_app_configure", "Update a stopped App configuration through Seizen validation.", agentAppConfigureInput{})
	addAgentTool(server, client, "seizen_app_run", "Start an App using Seizen's managed runtime.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_stop", "Stop an App and its managed process tree.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_restart", "Restart an App using Seizen's managed runtime.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_status", "Read verified App process and healthcheck status.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_set_preview", "Set a validated App preview URL.", agentSetPreviewInput{})
	addAgentTool(server, client, "seizen_app_run_tests", "Run the configured App tests in a managed process.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_get_logs", "Read bounded logs from the managed App runtime.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_capture_preview", "Capture the web App preview when Playwright is available.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_get_console_errors", "Read browser console errors when Playwright is available.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_smoke_test", "Load the App preview in a managed browser and report status, console errors and timing.", agentAppIDInput{})
	addAgentTool(server, client, "seizen_app_test_route", "Load a specific route of the web App preview and report status and console errors.", agentAppTestRouteInput{})
	addAgentTool(server, client, "seizen_app_discover", "Inspect the authorized project and return App candidates without persisting them.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_app_mount", "Configure and start an App through Seizen, then wait for verified readiness.", agentAppMountInput{})
	addAgentTool(server, client, "seizen_app_wait_ready", "Wait for the managed App process and web endpoint or desktop process to be verified.", agentAppWaitReadyInput{})
	addAgentTool(server, client, "seizen_app_attach_running", "Attach an App only to a user-confirmed terminal managed by this project.", agentAppAttachInput{})
	addAgentTool(server, client, "seizen_app_get_runtime_diagnostics", "Return the verified process, run, endpoint, logs, exit and optional console diagnostics.", agentAppIDInput{})
	addAgentExperimentTools(server, client)
	addAgentServerTools(server, client)
	addAgentTool(server, client, "seizen_desk_open", "Open a project document (PDF, Word, image, video, text) or an HTTP(S) URL as a panel on the user's board.", agentDeskOpenInput{})
	addAgentTool(server, client, "seizen_desk_add_note", "Place a markdown note panel on the user's board.", agentDeskNoteInput{})
	addAgentTool(server, client, "seizen_desk_add_todo", "Place a to-do checklist panel on the user's board.", agentDeskTodoInput{})
	addAgentTool(server, client, "seizen_desk_tidy", "Arrange the panels on the user's board neatly.", agentEmptyInput{})
	addAgentTool(server, client, "seizen_files_list", "List files and folders under a project-relative path (read-only).", agentFilesListInput{})
	addAgentTool(server, client, "seizen_files_move", "Move a file or folder within the project. Requires single-use user approval.", agentFilesMoveInput{})
	addAgentTool(server, client, "seizen_files_rename", "Rename a file or folder within the project. Requires single-use user approval.", agentFilesRenameInput{})
	return server
}

func addAgentTool[Input any](server *mcp.Server, client *agentRPCClient, name, description string, _ Input) {
	mcp.AddTool(server, &mcp.Tool{Name: name, Description: description},
		func(ctx context.Context, _ *mcp.CallToolRequest, input Input) (*mcp.CallToolResult, any, error) {
			output, err := client.Call(ctx, name, input)
			return nil, output, err
		})
}
