package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/massivemoose/ovek/internal/brainapi"
)

var ErrNotConfigured = errors.New("client is not configured")

type APIError struct {
	StatusCode int
	Code       string
	Message    string
}

func (err *APIError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Code != "" && err.Message != "" {
		return fmt.Sprintf("%s: %s", err.Code, err.Message)
	}
	if err.Message != "" {
		return err.Message
	}
	if err.Code != "" {
		return err.Code
	}
	return fmt.Sprintf("unexpected API status %d", err.StatusCode)
}

type Client struct {
	baseURL      *url.URL
	apiKey       string
	reauthToken  string
	httpClient   *http.Client
	streamClient *http.Client
}

type Option func(*Client)

func WithHTTPClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.httpClient = httpClient
	}
}

func WithStreamClient(httpClient *http.Client) Option {
	return func(client *Client) {
		client.streamClient = httpClient
	}
}

func New(baseURL string, apiKey string, options ...Option) (*Client, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, ErrNotConfigured
	}

	parsedURL, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("parse base URL: %w", err)
	}

	client := &Client{
		baseURL:      parsedURL,
		apiKey:       strings.TrimSpace(apiKey),
		httpClient:   &http.Client{},
		streamClient: &http.Client{Timeout: 0},
	}
	for _, option := range options {
		if option != nil {
			option(client)
		}
	}

	return client, nil
}

func (client *Client) Ping(ctx context.Context) error {
	request, err := client.newRequest(ctx, http.MethodGet, "/v1/ping", nil)
	if err != nil {
		return err
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return err
	}

	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read ping response: %w", err)
	}
	if strings.TrimSpace(string(body)) != "pong" {
		return fmt.Errorf("unexpected ping response %q", strings.TrimSpace(string(body)))
	}

	return nil
}

func (client *Client) Bootstrap(ctx context.Context, requestBody brainapi.BootstrapAuthRequest) (brainapi.BootstrapAuthResponse, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return brainapi.BootstrapAuthResponse{}, fmt.Errorf("marshal bootstrap request: %w", err)
	}

	request, err := client.newRequest(ctx, http.MethodPost, "/v1/auth/bootstrap", bytes.NewReader(payload))
	if err != nil {
		return brainapi.BootstrapAuthResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return brainapi.BootstrapAuthResponse{}, err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return brainapi.BootstrapAuthResponse{}, err
	}

	var bootstrapResponse brainapi.BootstrapAuthResponse
	if err := json.NewDecoder(response.Body).Decode(&bootstrapResponse); err != nil {
		return brainapi.BootstrapAuthResponse{}, fmt.Errorf("decode bootstrap response: %w", err)
	}

	return bootstrapResponse, nil
}

func (client *Client) Reauth(ctx context.Context, password string) (brainapi.ReauthResponse, error) {
	payload, err := json.Marshal(brainapi.ReauthRequest{Password: password})
	if err != nil {
		return brainapi.ReauthResponse{}, fmt.Errorf("marshal reauth request: %w", err)
	}

	request, err := client.newRequest(ctx, http.MethodPost, "/v1/auth/reauth", bytes.NewReader(payload))
	if err != nil {
		return brainapi.ReauthResponse{}, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return brainapi.ReauthResponse{}, err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return brainapi.ReauthResponse{}, err
	}

	var reauthResponse brainapi.ReauthResponse
	if err := json.NewDecoder(response.Body).Decode(&reauthResponse); err != nil {
		return brainapi.ReauthResponse{}, fmt.Errorf("decode reauth response: %w", err)
	}

	return reauthResponse, nil
}

func (client *Client) GetProjects(ctx context.Context, limit int) ([]brainapi.ProjectSummary, error) {
	responseBody, err := client.getJSON(ctx, withLimit("/v1/projects", limit))
	if err != nil {
		return nil, err
	}

	var projects []brainapi.ProjectSummary
	if err := json.Unmarshal(responseBody, &projects); err != nil {
		return nil, fmt.Errorf("decode projects response: %w", err)
	}

	return projects, nil
}

func (client *Client) GetProject(ctx context.Context, projectName string) (brainapi.ProjectSummary, error) {
	responseBody, err := client.getJSON(ctx, "/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName)))
	if err != nil {
		return brainapi.ProjectSummary{}, err
	}

	var project brainapi.ProjectSummary
	if err := json.Unmarshal(responseBody, &project); err != nil {
		return brainapi.ProjectSummary{}, fmt.Errorf("decode project response: %w", err)
	}

	return project, nil
}

func (client *Client) GetProjectDeployments(ctx context.Context, projectName string, limit int) ([]brainapi.Deployment, error) {
	responseBody, err := client.getJSON(ctx, withLimit("/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/deployments", limit))
	if err != nil {
		return nil, err
	}

	var deployments []brainapi.Deployment
	if err := json.Unmarshal(responseBody, &deployments); err != nil {
		return nil, fmt.Errorf("decode deployments response: %w", err)
	}

	return deployments, nil
}

func (client *Client) GetProjectJobs(ctx context.Context, projectName string, limit int) ([]brainapi.Job, error) {
	responseBody, err := client.getJSON(ctx, withLimit("/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/jobs", limit))
	if err != nil {
		return nil, err
	}

	var jobs []brainapi.Job
	if err := json.Unmarshal(responseBody, &jobs); err != nil {
		return nil, fmt.Errorf("decode jobs response: %w", err)
	}

	return jobs, nil
}

func (client *Client) GetProjectRuntime(ctx context.Context, projectName string) (brainapi.ProjectRuntime, error) {
	responseBody, err := client.getJSON(ctx, "/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/runtime")
	if err != nil {
		return brainapi.ProjectRuntime{}, err
	}

	var runtime brainapi.ProjectRuntime
	if err := json.Unmarshal(responseBody, &runtime); err != nil {
		return brainapi.ProjectRuntime{}, fmt.Errorf("decode runtime response: %w", err)
	}

	return runtime, nil
}

func (client *Client) CreateDeployment(ctx context.Context, projectName string, requestBody brainapi.CreateDeploymentRequest) (brainapi.Job, error) {
	payload, err := json.Marshal(requestBody)
	if err != nil {
		return brainapi.Job{}, fmt.Errorf("marshal deployment request: %w", err)
	}

	request, err := client.newRequest(
		ctx,
		http.MethodPost,
		"/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/deployments",
		bytes.NewReader(payload),
	)
	if err != nil {
		return brainapi.Job{}, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := client.httpClient.Do(request)
	if err != nil {
		return brainapi.Job{}, err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return brainapi.Job{}, err
	}

	var job brainapi.Job
	if err := json.NewDecoder(response.Body).Decode(&job); err != nil {
		return brainapi.Job{}, fmt.Errorf("decode deployment response: %w", err)
	}

	return job, nil
}

func (client *Client) GetJob(ctx context.Context, jobID string) (brainapi.Job, error) {
	responseBody, err := client.getJSON(ctx, brainapi.JobPath(strings.TrimSpace(jobID)))
	if err != nil {
		return brainapi.Job{}, err
	}

	var job brainapi.Job
	if err := json.Unmarshal(responseBody, &job); err != nil {
		return brainapi.Job{}, fmt.Errorf("decode job response: %w", err)
	}

	return job, nil
}

func (client *Client) GetJobLogs(ctx context.Context, jobID string) ([]byte, error) {
	return client.getText(ctx, brainapi.JobLogsPath(strings.TrimSpace(jobID)))
}

func (client *Client) StreamJobLogs(ctx context.Context, jobID string) (io.ReadCloser, error) {
	return client.openStream(ctx, brainapi.JobLogsStreamPath(strings.TrimSpace(jobID)))
}

func (client *Client) GetRuntimeLogs(ctx context.Context, projectName string) ([]byte, error) {
	return client.getText(ctx, "/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/runtime/logs")
}

func (client *Client) StreamRuntimeLogs(ctx context.Context, projectName string) (io.ReadCloser, error) {
	return client.openStream(ctx, "/v1/projects/"+url.PathEscape(strings.TrimSpace(projectName))+"/runtime/logs/stream")
}

func ReadSSEData(logs io.Reader, emit func(string) error) error {
	scanner := bufio.NewScanner(logs)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		if err := emit(strings.TrimPrefix(line, "data: ")); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read SSE stream: %w", err)
	}

	return nil
}

func (client *Client) getJSON(ctx context.Context, requestPath string) ([]byte, error) {
	request, err := client.newRequest(ctx, http.MethodGet, requestPath, nil)
	if err != nil {
		return nil, err
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return nil, err
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read JSON response: %w", err)
	}

	return responseBody, nil
}

func (client *Client) getText(ctx context.Context, requestPath string) ([]byte, error) {
	request, err := client.newRequest(ctx, http.MethodGet, requestPath, nil)
	if err != nil {
		return nil, err
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	if err := decodeAPIError(response); err != nil {
		return nil, err
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("read text response: %w", err)
	}

	return responseBody, nil
}

func (client *Client) openStream(ctx context.Context, requestPath string) (io.ReadCloser, error) {
	request, err := client.newRequest(ctx, http.MethodGet, requestPath, nil)
	if err != nil {
		return nil, err
	}

	response, err := client.streamClient.Do(request)
	if err != nil {
		return nil, err
	}

	if err := decodeAPIError(response); err != nil {
		response.Body.Close()
		return nil, err
	}

	return response.Body, nil
}

func (client *Client) newRequest(ctx context.Context, method string, requestPath string, body io.Reader) (*http.Request, error) {
	if client == nil || client.baseURL == nil {
		return nil, ErrNotConfigured
	}

	relativeURL, err := url.Parse(requestPath)
	if err != nil {
		return nil, fmt.Errorf("parse request path: %w", err)
	}
	requestURL := client.baseURL.ResolveReference(relativeURL)

	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	if client.apiKey != "" {
		request.Header.Set("X-API-Key", client.apiKey)
	}
	if client.reauthToken != "" {
		request.Header.Set("X-Ovek-Reauth-Token", client.reauthToken)
	}

	return request, nil
}

func (client *Client) SetReauthToken(token string) {
	client.reauthToken = strings.TrimSpace(token)
}

func decodeAPIError(response *http.Response) error {
	if response.StatusCode >= http.StatusOK && response.StatusCode < http.StatusMultipleChoices {
		return nil
	}

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read API error response: %w", err)
	}

	var apiError brainapi.APIError
	if err := json.Unmarshal(responseBody, &apiError); err != nil {
		return &APIError{
			StatusCode: response.StatusCode,
			Message:    strings.TrimSpace(string(responseBody)),
		}
	}

	return &APIError{
		StatusCode: response.StatusCode,
		Code:       apiError.Code,
		Message:    apiError.Message,
	}
}

func withLimit(requestPath string, limit int) string {
	if limit <= 0 {
		return requestPath
	}
	return fmt.Sprintf("%s?limit=%d", requestPath, limit)
}
