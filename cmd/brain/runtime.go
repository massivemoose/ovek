package main

import (
	"context"
	"fmt"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockernetwork "github.com/docker/docker/api/types/network"
	dockerclient "github.com/docker/docker/client"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	managedLabelKey            = "alces.managed"
	projectLabelKey            = "alces.project"
	roleLabelKey               = "alces.role"
	deploymentLabelKey         = "alces.deployment"
	jobLabelKey                = "alces.job"
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
	ContainerInspect(ctx context.Context, containerID string) (dockercontainer.InspectResponse, error)
	ContainerCreate(ctx context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error)
	ContainerStart(ctx context.Context, containerID string, options dockercontainer.StartOptions) error
}

type dockerRuntime struct {
	client dockerClient
}

func newDockerRuntimeFromEnv() (*dockerRuntime, error) {
	client, err := dockerclient.NewClientWithOpts(
		dockerclient.FromEnv,
		dockerclient.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("create docker client: %w", err)
	}

	return &dockerRuntime{client: client}, nil
}

func newDockerRuntime(client dockerClient) *dockerRuntime {
	return &dockerRuntime{client: client}
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

func pocketBaseContainerName(projectName string) string {
	return "alces-" + projectName + "-pb"
}

func appContainerName(projectName string, deploymentID string) string {
	return "alces-" + projectName + "-app-" + deploymentID
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
		return fmt.Errorf("%s already exists but is not managed by alces", resourceName)
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
