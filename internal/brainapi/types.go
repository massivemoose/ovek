package brainapi

const JobTypeDeployment = "deployment"

type APIError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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
}

type JobLinks struct {
	Self       string `json:"self"`
	Logs       string `json:"logs"`
	LogsStream string `json:"logsStream"`
}

type Job struct {
	ID           string   `json:"id"`
	Type         string   `json:"type"`
	ProjectName  string   `json:"projectName"`
	RepoURL      string   `json:"repoUrl"`
	Status       string   `json:"status"`
	LogPath      string   `json:"logPath,omitempty"`
	ImageRef     string   `json:"imageRef,omitempty"`
	ErrorMessage string   `json:"errorMessage,omitempty"`
	CreatedAt    string   `json:"createdAt"`
	StartedAt    string   `json:"startedAt,omitempty"`
	FinishedAt   string   `json:"finishedAt,omitempty"`
	Links        JobLinks `json:"links"`
}

type CreateDeploymentRequest struct {
	RepoURL string `json:"repoUrl"`
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
