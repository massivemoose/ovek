package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

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
	return ensureProjectPocketBase(ctx, runtime, runtime, projectName, image, projectsHostDataDir)
}

func (runtime *podmanRuntime) EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error) {
	return ensureProjectPocketBase(ctx, runtime, runtime.dockerRuntime, projectName, image, projectsHostDataDir)
}

func ensureProjectPocketBase(ctx context.Context, imageRuntime Runtime, containerRuntime *dockerRuntime, projectName string, image string, projectsHostDataDir string) (string, error) {
	network, err := containerRuntime.EnsureProjectNetwork(ctx, projectName)
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

	container, err := containerRuntime.client.ContainerInspect(ctx, spec.Name)
	if err == nil {
		if err := validateExistingPocketBaseContainer(container, spec); err != nil {
			return "", err
		}
		if container.State != nil && !container.State.Running {
			if err := containerRuntime.client.ContainerStart(ctx, container.ID, dockercontainer.StartOptions{}); err != nil {
				return "", fmt.Errorf("start PocketBase container %q: %w", spec.Name, err)
			}
		}

		return container.ID, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspect PocketBase container %q: %w", spec.Name, err)
	}

	if err := imageRuntime.PullImage(ctx, spec.Config.Image); err != nil {
		return "", err
	}

	createResponse, err := containerRuntime.client.ContainerCreate(
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

	if err := containerRuntime.client.ContainerStart(ctx, createResponse.ID, dockercontainer.StartOptions{}); err != nil {
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
	if !equivalentContainerImageRef(container.Config.Image, spec.Config.Image) {
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

func (runtime *dockerRuntime) RemoveProjectPocketBase(ctx context.Context, projectName string) error {
	containerName := pocketBaseContainerName(projectName)
	container, err := runtime.client.ContainerInspect(ctx, containerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("inspect PocketBase container %q: %w", containerName, err)
	}
	if container.Config == nil {
		return fmt.Errorf("PocketBase container %q is missing config", containerName)
	}
	if err := requireManagedResourceOwnership(containerName, container.Config.Labels, managedResourceMetadata{
		ProjectName: projectName,
		Role:        resourceRolePocketBase,
	}); err != nil {
		return err
	}

	return runtime.removeManagedContainer(ctx, containerName, container, "PocketBase container")
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

func equivalentContainerImageRef(actual string, expected string) bool {
	return canonicalContainerImageRef(actual) == canonicalContainerImageRef(expected)
}

func canonicalContainerImageRef(imageRef string) string {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return ""
	}

	digest := ""
	if at := strings.Index(imageRef, "@"); at >= 0 {
		digest = imageRef[at:]
		imageRef = imageRef[:at]
	}

	tag := ""
	lastSlash := strings.LastIndex(imageRef, "/")
	lastColon := strings.LastIndex(imageRef, ":")
	if lastColon > lastSlash {
		tag = imageRef[lastColon+1:]
		imageRef = imageRef[:lastColon]
	}

	parts := strings.Split(imageRef, "/")
	host := "docker.io"
	pathParts := parts
	explicitHost := false
	if len(parts) > 1 {
		firstPart := parts[0]
		if strings.Contains(firstPart, ".") || strings.Contains(firstPart, ":") || firstPart == "localhost" {
			host = firstPart
			pathParts = parts[1:]
			explicitHost = true
		}
	}
	if !explicitHost && len(pathParts) == 1 {
		pathParts = append([]string{"library"}, pathParts[0])
	}

	canonical := host + "/" + strings.Join(pathParts, "/")
	if digest != "" {
		return canonical + digest
	}
	if tag == "" {
		tag = "latest"
	}

	return canonical + ":" + tag
}
