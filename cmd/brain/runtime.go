package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	dockertypes "github.com/docker/docker/api/types"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	managedLabelKey            = "ovek.managed"
	projectLabelKey            = "ovek.project"
	roleLabelKey               = "ovek.role"
	deploymentLabelKey         = "ovek.deployment"
	jobLabelKey                = "ovek.job"
	managedLabelValue          = "true"
	resourceRoleProjectNetwork = "project-network"
	resourceRolePocketBase     = "pocketbase"
	resourceRoleApp            = "app"
	projectNetworkDriver       = "bridge"
)

type managedResourceMetadata struct {
	ProjectName  string
	Role         string
	DeploymentID string
	JobID        string
}

type projectNetwork struct {
	ID   string
	Name string
}

type dockerClient interface {
	NetworkInspect(ctx context.Context, networkID string, options dockernetwork.InspectOptions) (dockernetwork.Inspect, error)
	NetworkCreate(ctx context.Context, name string, options dockernetwork.CreateOptions) (dockernetwork.CreateResponse, error)
	NetworkConnect(ctx context.Context, networkID, containerID string, config *dockernetwork.EndpointSettings) error
	NetworkRemove(ctx context.Context, networkID string) error
	ContainerList(ctx context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error)
	ContainerInspect(ctx context.Context, containerID string) (dockercontainer.InspectResponse, error)
	ContainerLogs(ctx context.Context, container string, options dockercontainer.LogsOptions) (io.ReadCloser, error)
	ContainerExecCreate(ctx context.Context, containerID string, options dockercontainer.ExecOptions) (dockercontainer.ExecCreateResponse, error)
	ContainerExecAttach(ctx context.Context, execID string, config dockercontainer.ExecAttachOptions) (dockertypes.HijackedResponse, error)
	ContainerExecInspect(ctx context.Context, execID string) (dockercontainer.ExecInspect, error)
	ImagePull(ctx context.Context, refStr string, options dockerimage.PullOptions) (io.ReadCloser, error)
	ContainerCreate(ctx context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options dockercontainer.StartOptions) error
	ContainerStop(ctx context.Context, containerID string, options dockercontainer.StopOptions) error
	ContainerRemove(ctx context.Context, containerID string, options dockercontainer.RemoveOptions) error
}

type dockerRuntime struct {
	client      dockerClient
	dialContext dialContextFunc
	sleep       sleepFunc
	hostname    func() (string, error)
}

type podmanImagePuller interface {
	PullImage(ctx context.Context, imageRef string, registryInsecure bool) error
}

type podmanRuntime struct {
	*dockerRuntime
	puller           podmanImagePuller
	registryInsecure bool
}

type podmanServiceImagePuller struct {
	client  *http.Client
	baseURL string
}

func newRuntimeFromConfig(cfg config) (Runtime, error) {
	switch cfg.RuntimeEngine {
	case runtimeEnginePodman:
		return newPodmanRuntime(cfg.RuntimeHost, cfg.RegistryInsecure)
	default:
		return nil, fmt.Errorf("unsupported runtime engine %q", cfg.RuntimeEngine)
	}
}

func newPodmanRuntime(runtimeHost string, registryInsecure bool) (*podmanRuntime, error) {
	client, err := newDockerCompatClient(runtimeHost)
	if err != nil {
		return nil, fmt.Errorf("create podman client: %w", err)
	}
	puller, err := newPodmanImagePuller(runtimeHost)
	if err != nil {
		return nil, fmt.Errorf("create podman pull client: %w", err)
	}

	return &podmanRuntime{
		dockerRuntime: &dockerRuntime{
			client:      client,
			dialContext: (&net.Dialer{Timeout: appReadinessDialTime}).DialContext,
			sleep:       sleepWithContext,
			hostname:    os.Hostname,
		},
		puller:           puller,
		registryInsecure: registryInsecure,
	}, nil
}

func newDockerCompatClient(runtimeHost string) (*dockerclient.Client, error) {
	options := []dockerclient.Opt{
		dockerclient.WithAPIVersionNegotiation(),
	}
	if runtimeHost == "" {
		options = append(options, dockerclient.FromEnv)
	} else {
		options = append(options, dockerclient.WithHost(runtimeHost))
	}

	return dockerclient.NewClientWithOpts(options...)
}

func newDockerRuntime(client dockerClient) *dockerRuntime {
	return &dockerRuntime{
		client:      client,
		dialContext: (&net.Dialer{Timeout: appReadinessDialTime}).DialContext,
		sleep:       sleepWithContext,
		hostname:    os.Hostname,
	}
}

func (runtime *dockerRuntime) PullImage(ctx context.Context, imageRef string) error {
	pullResponse, err := runtime.client.ImagePull(ctx, imageRef, dockerimage.PullOptions{})
	if err != nil {
		return fmt.Errorf("pull image %q: %w", imageRef, err)
	}
	defer pullResponse.Close()

	if _, err := io.Copy(io.Discard, pullResponse); err != nil {
		return fmt.Errorf("read image pull response for %q: %w", imageRef, err)
	}

	return nil
}

func (runtime *podmanRuntime) PullImage(ctx context.Context, imageRef string) error {
	if err := runtime.puller.PullImage(ctx, imageRef, runtime.registryInsecure); err != nil {
		return fmt.Errorf("pull image %q: %w", imageRef, err)
	}

	return nil
}

func newPodmanImagePuller(runtimeHost string) (podmanImagePuller, error) {
	if strings.TrimSpace(runtimeHost) == "" {
		runtimeHost = defaultPodmanRuntimeHost
	}

	parsedHost, err := url.Parse(runtimeHost)
	if err != nil {
		return nil, fmt.Errorf("parse podman runtime host %q: %w", runtimeHost, err)
	}

	switch parsedHost.Scheme {
	case "unix":
		socketPath := parsedHost.Path
		if socketPath == "" {
			socketPath = parsedHost.Opaque
		}
		if socketPath == "" {
			return nil, fmt.Errorf("podman runtime host %q is missing a unix socket path", runtimeHost)
		}

		return &podmanServiceImagePuller{
			client: &http.Client{
				Transport: &http.Transport{
					DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
						return (&net.Dialer{}).DialContext(ctx, "unix", socketPath)
					},
				},
			},
			baseURL: "http://d",
		}, nil
	case "tcp":
		if parsedHost.Host == "" {
			return nil, fmt.Errorf("podman runtime host %q is missing a tcp host", runtimeHost)
		}

		return &podmanServiceImagePuller{
			client:  &http.Client{},
			baseURL: "http://" + parsedHost.Host,
		}, nil
	case "http", "https":
		return &podmanServiceImagePuller{
			client:  &http.Client{},
			baseURL: strings.TrimRight(runtimeHost, "/"),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported podman runtime host scheme %q", parsedHost.Scheme)
	}
}

func (puller *podmanServiceImagePuller) PullImage(ctx context.Context, imageRef string, registryInsecure bool) error {
	endpoint, err := url.Parse(puller.baseURL)
	if err != nil {
		return fmt.Errorf("parse podman service base URL %q: %w", puller.baseURL, err)
	}
	endpoint = endpoint.ResolveReference(&url.URL{Path: "/v1.0.0/libpod/images/pull"})

	query := endpoint.Query()
	query.Set("reference", imageRef)
	if registryInsecure {
		query.Set("tlsVerify", "false")
	}
	endpoint.RawQuery = query.Encode()

	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint.String(), nil)
	if err != nil {
		return fmt.Errorf("build podman image pull request: %w", err)
	}

	response, err := puller.client.Do(request)
	if err != nil {
		return fmt.Errorf("call podman image pull API: %w", err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read podman image pull response: %w", err)
	}

	if response.StatusCode >= http.StatusBadRequest {
		message := strings.TrimSpace(string(responseBody))
		if message == "" {
			message = response.Status
		}

		return fmt.Errorf("podman image pull API returned %s: %s", response.Status, message)
	}
	if err := podmanPullResponseError(responseBody); err != nil {
		return fmt.Errorf("podman image pull failed: %w", err)
	}

	return nil
}

func podmanPullResponseError(responseBody []byte) error {
	for _, line := range strings.Split(string(responseBody), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		var payload map[string]any
		if err := json.Unmarshal([]byte(line), &payload); err != nil {
			continue
		}
		for _, key := range []string{"error", "errorMessage", "cause"} {
			value, ok := payload[key].(string)
			if ok && strings.TrimSpace(value) != "" {
				return errors.New(strings.TrimSpace(value))
			}
		}
	}

	return nil
}

func (runtime *dockerRuntime) removeManagedContainer(ctx context.Context, containerName string, container dockercontainer.InspectResponse, resourceType string) error {
	containerID := container.ID
	if containerID == "" {
		containerID = containerName
	}

	if container.State != nil && container.State.Running {
		timeout := appStopTimeoutSeconds
		if err := runtime.client.ContainerStop(ctx, containerID, dockercontainer.StopOptions{
			Timeout: &timeout,
		}); err != nil && !cerrdefs.IsNotFound(err) {
			return fmt.Errorf("stop %s %q: %w", resourceType, containerName, err)
		}
	}

	if err := runtime.client.ContainerRemove(ctx, containerID, dockercontainer.RemoveOptions{}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove %s %q: %w", resourceType, containerName, err)
	}

	return nil
}

func (runtime *dockerRuntime) EnsureProjectNetwork(ctx context.Context, projectName string) (projectNetwork, error) {
	networkName := projectNetworkName(projectName)
	metadata := managedResourceMetadata{
		ProjectName: projectName,
		Role:        resourceRoleProjectNetwork,
	}

	network, err := runtime.client.NetworkInspect(ctx, networkName, dockernetwork.InspectOptions{})
	if err == nil {
		if err := requireManagedResourceOwnership(networkName, network.Labels, metadata); err != nil {
			return projectNetwork{}, err
		}

		return projectNetwork{
			ID:   network.ID,
			Name: network.Name,
		}, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return projectNetwork{}, fmt.Errorf("inspect project network %q: %w", networkName, err)
	}

	createResponse, err := runtime.client.NetworkCreate(ctx, networkName, dockernetwork.CreateOptions{
		Driver: projectNetworkDriver,
		Labels: managedLabels(metadata),
	})
	if err != nil {
		return projectNetwork{}, fmt.Errorf("create project network %q: %w", networkName, err)
	}

	return projectNetwork{
		ID:   createResponse.ID,
		Name: networkName,
	}, nil
}

func projectNetworkName(projectName string) string {
	return projectName + "-net"
}

func (runtime *dockerRuntime) RemoveProjectNetwork(ctx context.Context, projectName string) error {
	networkName := projectNetworkName(projectName)
	network, err := runtime.client.NetworkInspect(ctx, networkName, dockernetwork.InspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("inspect project network %q: %w", networkName, err)
	}
	if err := requireManagedResourceOwnership(networkName, network.Labels, managedResourceMetadata{
		ProjectName: projectName,
		Role:        resourceRoleProjectNetwork,
	}); err != nil {
		return err
	}

	networkID := network.ID
	if networkID == "" {
		networkID = networkName
	}

	if err := runtime.client.NetworkRemove(ctx, networkID); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("remove project network %q: %w", networkName, err)
	}

	return nil
}

func pocketBaseContainerName(projectName string) string {
	return "ovek-" + projectName + "-pb"
}

func appContainerName(projectName string, deploymentID string) string {
	return "ovek-" + projectName + "-app-" + deploymentID
}

func managedLabels(metadata managedResourceMetadata) map[string]string {
	labels := map[string]string{
		managedLabelKey: managedLabelValue,
		projectLabelKey: metadata.ProjectName,
		roleLabelKey:    metadata.Role,
	}

	if metadata.DeploymentID != "" {
		labels[deploymentLabelKey] = metadata.DeploymentID
	}
	if metadata.JobID != "" {
		labels[jobLabelKey] = metadata.JobID
	}

	return labels
}

func requireManagedResourceOwnership(resourceName string, labels map[string]string, metadata managedResourceMetadata) error {
	if labels[managedLabelKey] != managedLabelValue {
		return fmt.Errorf("%s already exists but is not managed by ovek", resourceName)
	}
	if labels[projectLabelKey] != metadata.ProjectName {
		return fmt.Errorf("%s already exists for project %q, not %q", resourceName, labels[projectLabelKey], metadata.ProjectName)
	}
	if labels[roleLabelKey] != metadata.Role {
		return fmt.Errorf("%s already exists with role %q, not %q", resourceName, labels[roleLabelKey], metadata.Role)
	}
	if metadata.DeploymentID != "" && labels[deploymentLabelKey] != metadata.DeploymentID {
		return fmt.Errorf("%s already exists for deployment %q, not %q", resourceName, labels[deploymentLabelKey], metadata.DeploymentID)
	}
	if metadata.JobID != "" && labels[jobLabelKey] != metadata.JobID {
		return fmt.Errorf("%s already exists for job %q, not %q", resourceName, labels[jobLabelKey], metadata.JobID)
	}

	return nil
}
