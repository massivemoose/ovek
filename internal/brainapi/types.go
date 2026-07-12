package brainapi

import "encoding/json"

const (
	JobTypeDeployment  = "deployment"
	JobSourceTypeRepo  = "repo"
	JobSourceTypeImage = "image"

	WorkflowRunStatusQueued    = "queued"
	WorkflowRunStatusPreparing = "preparing"
	WorkflowRunStatusRunning   = "running"
	WorkflowRunStatusSucceeded = "succeeded"
	WorkflowRunStatusFailed    = "failed"
	WorkflowRunStatusSkipped   = "skipped"
	WorkflowRunStatusCanceled  = "canceled"
	WorkflowRunStatusTimedOut  = "timed_out"

	WorkflowRunTriggerManual   = "manual"
	WorkflowRunTriggerSchedule = "schedule"
	WorkflowRunTriggerAPI      = "api"
)

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type BootstrapAuthRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type BootstrapAuthResponse struct {
	Username string `json:"username"`
	APIKey   string `json:"apiKey"`
}

type ReauthRequest struct {
	Password string `json:"password"`
}

type ReauthResponse struct {
	ReauthToken string `json:"reauthToken"`
	ExpiresAt   string `json:"expiresAt"`
}

type APIKeySummary struct {
	ID         string `json:"id"`
	Label      string `json:"label"`
	CreatedAt  string `json:"createdAt"`
	LastUsedAt string `json:"lastUsedAt,omitempty"`
	RevokedAt  string `json:"revokedAt,omitempty"`
}

type CreateAPIKeyRequest struct {
	Label string `json:"label"`
}

type CreateAPIKeyResponse struct {
	ID        string `json:"id"`
	Label     string `json:"label"`
	APIKey    string `json:"apiKey"`
	CreatedAt string `json:"createdAt"`
}

type ChangePasswordRequest struct {
	CurrentPassword string `json:"currentPassword"`
	NewPassword     string `json:"newPassword"`
}

type RegistryCredential struct {
	Host      string `json:"host"`
	Username  string `json:"username"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

type UpsertRegistryCredentialRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type ProjectSummary struct {
	Name                string  `json:"name"`
	Status              string  `json:"status"`
	CurrentDeploymentID *string `json:"currentDeploymentId"`
	CreatedAt           string  `json:"createdAt"`
}

type Deployment struct {
	ID                      string `json:"id"`
	ProjectName             string `json:"projectName"`
	ImageRef                string `json:"imageRef"`
	SourceType              string `json:"sourceType,omitempty"`
	SourceRef               string `json:"sourceRef,omitempty"`
	AppContainerName        string `json:"appContainerName"`
	NetworkName             string `json:"networkName"`
	PocketBaseContainerName string `json:"pocketBaseContainerName"`
	Status                  string `json:"status"`
	CreatedAt               string `json:"createdAt"`
	ConfigRevisionID        string `json:"configRevisionId,omitempty"`
}

type JobLinks struct {
	Self       string `json:"self"`
	Logs       string `json:"logs"`
	LogsStream string `json:"logsStream"`
}

type Job struct {
	ID               string   `json:"id"`
	Type             string   `json:"type"`
	ProjectName      string   `json:"projectName"`
	RepoURL          string   `json:"repoUrl"`
	SourceType       string   `json:"sourceType,omitempty"`
	SourceRef        string   `json:"sourceRef,omitempty"`
	Status           string   `json:"status"`
	Phase            string   `json:"phase,omitempty"`
	LogPath          string   `json:"logPath,omitempty"`
	ImageRef         string   `json:"imageRef,omitempty"`
	ErrorMessage     string   `json:"errorMessage,omitempty"`
	CreatedAt        string   `json:"createdAt"`
	StartedAt        string   `json:"startedAt,omitempty"`
	FinishedAt       string   `json:"finishedAt,omitempty"`
	ConfigRevisionID string   `json:"configRevisionId,omitempty"`
	Links            JobLinks `json:"links"`
}

type WorkflowLinks struct {
	Self string `json:"self"`
	Runs string `json:"runs"`
}

type Workflow struct {
	ProjectName        string        `json:"projectName"`
	Name               string        `json:"name"`
	SourceImageRef     string        `json:"sourceImageRef"`
	ResolvedRepoDigest string        `json:"resolvedRepoDigest,omitempty"`
	RuntimeImageID     string        `json:"runtimeImageId,omitempty"`
	Schedule           string        `json:"schedule,omitempty"`
	QueueCap           int           `json:"queueCap"`
	Enabled            bool          `json:"enabled"`
	CreatedAt          string        `json:"createdAt"`
	UpdatedAt          string        `json:"updatedAt"`
	Links              WorkflowLinks `json:"links"`
}

type WorkflowRunLinks struct {
	Self       string `json:"self"`
	Logs       string `json:"logs"`
	LogsStream string `json:"logsStream"`
}

type WorkflowRun struct {
	ID                 string           `json:"id"`
	ProjectName        string           `json:"projectName"`
	WorkflowName       string           `json:"workflowName"`
	TriggerType        string           `json:"triggerType"`
	Status             string           `json:"status"`
	ConfigRevisionID   string           `json:"configRevisionId,omitempty"`
	LogPath            string           `json:"logPath,omitempty"`
	Payload            json.RawMessage  `json:"-"`
	IdempotencyKey     string           `json:"idempotencyKey,omitempty"`
	TriggerTokenID     string           `json:"triggerTokenId,omitempty"`
	SourceImageRef     string           `json:"sourceImageRef"`
	ResolvedRepoDigest string           `json:"resolvedRepoDigest,omitempty"`
	RuntimeImageID     string           `json:"runtimeImageId,omitempty"`
	ExitCode           *int             `json:"exitCode,omitempty"`
	ErrorMessage       string           `json:"errorMessage,omitempty"`
	CreatedAt          string           `json:"createdAt"`
	StartedAt          string           `json:"startedAt,omitempty"`
	FinishedAt         string           `json:"finishedAt,omitempty"`
	Links              WorkflowRunLinks `json:"links"`
}

type UpsertWorkflowRequest struct {
	ImageRef string `json:"imageRef"`
	Schedule string `json:"schedule,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
	QueueCap int    `json:"queueCap,omitempty"`
}

type CreateWorkflowRunRequest struct {
	TriggerType string          `json:"triggerType,omitempty"`
	Payload     json.RawMessage `json:"payload,omitempty"`
}

type WorkflowTriggerTokenSummary struct {
	ID           string `json:"id"`
	ProjectName  string `json:"projectName"`
	WorkflowName string `json:"workflowName"`
	Label        string `json:"label"`
	CreatedAt    string `json:"createdAt"`
	LastUsedAt   string `json:"lastUsedAt,omitempty"`
	RevokedAt    string `json:"revokedAt,omitempty"`
}

type CreateWorkflowTriggerTokenRequest struct {
	Label string `json:"label"`
}

type CreateWorkflowTriggerTokenResponse struct {
	ID           string `json:"id"`
	ProjectName  string `json:"projectName"`
	WorkflowName string `json:"workflowName"`
	Label        string `json:"label"`
	Token        string `json:"token"`
	CreatedAt    string `json:"createdAt"`
}

type CreateDeploymentRequest struct {
	RepoURL string `json:"repoUrl"`
}

type CreateRunRequest struct {
	CapsuleRef string `json:"capsuleRef"`
}

type ProjectEnvironmentEntry struct {
	Name      string  `json:"name"`
	Secret    bool    `json:"secret"`
	Value     *string `json:"value"`
	UpdatedAt string  `json:"updatedAt"`
}

type SetProjectEnvironmentRequest struct {
	Value  string `json:"value"`
	Secret bool   `json:"secret"`
}

type ProjectEnvironmentMutation struct {
	RevisionID string                   `json:"revisionId"`
	Entry      *ProjectEnvironmentEntry `json:"entry,omitempty"`
}

type ProjectPocketBaseStatus struct {
	ProjectName          string  `json:"projectName"`
	ContainerName        string  `json:"containerName"`
	Running              bool    `json:"running"`
	Initialized          bool    `json:"initialized"`
	SuperuserEmail       *string `json:"superuserEmail"`
	AppSecretsConfigured bool    `json:"appSecretsConfigured"`
	AppSecretsRevisionID string  `json:"appSecretsRevisionId,omitempty"`
	UpdatedAt            string  `json:"updatedAt,omitempty"`
}

type InitProjectPocketBaseRequest struct {
	Email      string `json:"email,omitempty"`
	AppSecrets bool   `json:"appSecrets"`
}

type ProjectRuntimeApp struct {
	ContainerName string `json:"containerName"`
	ImageRef      string `json:"imageRef"`
	Running       bool   `json:"running"`
}

type ProjectRuntimeContainer struct {
	ContainerName string `json:"containerName"`
	Running       bool   `json:"running"`
}

type ProjectRuntimeNetwork struct {
	Name string `json:"name"`
}

type ProjectRuntime struct {
	ProjectName         string                   `json:"projectName"`
	CurrentDeploymentID *string                  `json:"currentDeploymentId"`
	App                 *ProjectRuntimeApp       `json:"app"`
	PocketBase          *ProjectRuntimeContainer `json:"pocketBase"`
	Network             *ProjectRuntimeNetwork   `json:"network"`
}
