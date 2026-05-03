package brainapi

const JobTypeDeployment = "deployment"

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

type CreateDeploymentRequest struct {
	RepoURL string `json:"repoUrl"`
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
