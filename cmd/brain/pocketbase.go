package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockermount "github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	pocketBaseDataDirName   = "pb_data"
	pocketBaseDataMountPath = "/pb_data"
	pocketBaseNetworkAlias  = "db"
)

type pocketBaseSpec struct {
	ProjectName         string
	Image               string
	ProjectsHostDataDir string
	Network             projectNetwork
}

type pocketBaseContainerSpec struct {
	Name             string
	Metadata         managedResourceMetadata
	Config           *dockercontainer.Config
	HostConfig       *dockercontainer.HostConfig
	NetworkingConfig *dockernetwork.NetworkingConfig
	HostDataDir      string
}

func (runtime *dockerRuntime) EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	network, err := runtime.EnsureProjectNetwork(ctx, projectName)
	if err != nil {
		return "", fmt.Errorf("ensure project network: %w", err)
	}

	spec := newPocketBaseContainerSpec(pocketBaseSpec{
		ProjectName:         projectName,
		Image:               image,
		ProjectsHostDataDir: projectsHostDataDir,
		Network:             network,
	})

	if err := os.MkdirAll(spec.HostDataDir, 0o755); err != nil {
		return "", fmt.Errorf("create PocketBase data directory %q: %w", spec.HostDataDir, err)
	}

	container, err := runtime.client.ContainerInspect(ctx, spec.Name)
	if err == nil {
		if err := validateExistingPocketBaseContainer(container, spec); err != nil {
			return "", err
		}
		if container.State != nil && !container.State.Running {
			if err := runtime.client.ContainerStart(ctx, container.ID, dockercontainer.StartOptions{}); err != nil {
				return "", fmt.Errorf("start PocketBase container %q: %w", spec.Name, err)
			}
		}

		return container.ID, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspect PocketBase container %q: %w", spec.Name, err)
	}

	createResponse, err := runtime.client.ContainerCreate(
		ctx,
		spec.Config,
		spec.HostConfig,
		spec.NetworkingConfig,
		(*ocispec.Platform)(nil),
		spec.Name,
	)
	if err != nil {
		return "", fmt.Errorf("create PocketBase container %q: %w", spec.Name, err)
	}

	if err := runtime.client.ContainerStart(ctx, createResponse.ID, dockercontainer.StartOptions{}); err != nil {
		return "", fmt.Errorf("start PocketBase container %q: %w", spec.Name, err)
	}

	return createResponse.ID, nil
}

func newPocketBaseContainerSpec(spec pocketBaseSpec) pocketBaseContainerSpec {
	metadata := managedResourceMetadata{
		ProjectName: spec.ProjectName,
		Role:        resourceRolePocketBase,
	}

	hostDataDir := pocketBaseDataDir(spec.ProjectsHostDataDir, spec.ProjectName)
	containerName := pocketBaseContainerName(spec.ProjectName)

	return pocketBaseContainerSpec{
		Name:        containerName,
		Metadata:    metadata,
		HostDataDir: hostDataDir,
		Config: &dockercontainer.Config{
			Image:  spec.Image,
			Labels: managedLabels(metadata),
		},
		HostConfig: &dockercontainer.HostConfig{
			NetworkMode: dockercontainer.NetworkMode(spec.Network.Name),
			RestartPolicy: dockercontainer.RestartPolicy{
				Name: dockercontainer.RestartPolicyUnlessStopped,
			},
			Mounts: []dockermount.Mount{
				{
					Type:   dockermount.TypeBind,
					Source: hostDataDir,
					Target: pocketBaseDataMountPath,
				},
			},
		},
		NetworkingConfig: &dockernetwork.NetworkingConfig{
			EndpointsConfig: map[string]*dockernetwork.EndpointSettings{
				spec.Network.Name: {
					Aliases: []string{pocketBaseNetworkAlias},
				},
			},
		},
	}
}

func validateExistingPocketBaseContainer(container dockercontainer.InspectResponse, spec pocketBaseContainerSpec) error {
	if container.Config == nil {
		return fmt.Errorf("PocketBase container %q is missing config", spec.Name)
	}
	if err := requireManagedResourceOwnership(spec.Name, container.Config.Labels, spec.Metadata); err != nil {
		return err
	}
	if container.Config.Image != spec.Config.Image {
		return fmt.Errorf("PocketBase container %q already exists with image %q, not %q", spec.Name, container.Config.Image, spec.Config.Image)
	}
	if !hasMount(container.Mounts, spec.HostDataDir, pocketBaseDataMountPath) {
		return fmt.Errorf("PocketBase container %q is missing expected data mount %q -> %q", spec.Name, spec.HostDataDir, pocketBaseDataMountPath)
	}
	if container.NetworkSettings == nil {
		return fmt.Errorf("PocketBase container %q is missing network settings", spec.Name)
	}

	networkName := string(spec.HostConfig.NetworkMode)
	endpoint := container.NetworkSettings.Networks[networkName]
	if endpoint == nil {
		return fmt.Errorf("PocketBase container %q is not attached to network %q", spec.Name, networkName)
	}
	if !slices.Contains(endpoint.Aliases, pocketBaseNetworkAlias) {
		return fmt.Errorf("PocketBase container %q is missing network alias %q", spec.Name, pocketBaseNetworkAlias)
	}

	return nil
}

func pocketBaseDataDir(projectsHostDataDir string, projectName string) string {
	return filepath.Join(projectsHostDataDir, projectName, pocketBaseDataDirName)
}

func hasMount(mounts []dockercontainer.MountPoint, source string, target string) bool {
	for _, mount := range mounts {
		if mount.Source == source && mount.Destination == target {
			return true
		}
	}

	return false
}
