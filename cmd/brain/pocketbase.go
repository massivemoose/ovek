package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockermount "github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	pocketBaseDataDirName   = "pb_data"
	pocketBaseDataMountPath = "/pb_data"
	pocketBaseNetworkAlias  = "db"
	pocketBaseRuntimePort   = "8090"
	pocketBaseDataUID       = 100
	pocketBaseDataGID       = 101

	pocketBaseReadinessTimeout  = 15 * time.Second
	pocketBaseReadinessInterval = 250 * time.Millisecond
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

	if err := ensurePocketBaseDataDir(spec.HostDataDir); err != nil {
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

func ensurePocketBaseDataDir(path string) error {
	if err := os.MkdirAll(path, 0o770); err != nil {
		return err
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(path, pocketBaseDataUID, pocketBaseDataGID); err != nil {
			return fmt.Errorf("set owner: %w", err)
		}
	}
	if err := os.Chmod(path, 0o770); err != nil {
		return fmt.Errorf("set permissions: %w", err)
	}

	return nil
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

func (runtime *dockerRuntime) WaitForProjectPocketBaseReady(ctx context.Context, projectName string) error {
	readyContext, cancel := context.WithTimeout(ctx, pocketBaseReadinessTimeout)
	defer cancel()

	address := ""
	var lastErr error
	for {
		if err := runtime.requireRunningManagedPocketBase(readyContext, projectName); err != nil {
			lastErr = err
		} else if err := runtime.ensureControlPlaneProjectNetworkAttachment(readyContext, projectName); err != nil {
			lastErr = err
		} else {
			target, err := runtime.resolveProjectPocketBaseReadinessTarget(readyContext, projectName)
			if err == nil {
				address = target
				conn, err := runtime.dialContext(readyContext, "tcp", address)
				if err == nil {
					if closeErr := conn.Close(); closeErr != nil {
						return fmt.Errorf("close PocketBase readiness probe connection: %w", closeErr)
					}

					return nil
				}
				lastErr = err
			} else {
				lastErr = err
			}
		}

		if errors.Is(readyContext.Err(), context.DeadlineExceeded) {
			break
		}

		if waitErr := runtime.sleep(readyContext, pocketBaseReadinessInterval); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
				break
			}

			return fmt.Errorf("wait for next PocketBase readiness probe: %w", waitErr)
		}
	}

	if lastErr == nil {
		lastErr = readyContext.Err()
	}

	return fmt.Errorf("timed out waiting for PocketBase container %q to accept TCP connections on %s: %w", pocketBaseContainerName(projectName), address, lastErr)
}

func (runtime *dockerRuntime) resolveProjectPocketBaseReadinessTarget(ctx context.Context, projectName string) (string, error) {
	containerName := pocketBaseContainerName(projectName)
	container, err := runtime.client.ContainerInspect(ctx, containerName)
	if err != nil {
		return "", fmt.Errorf("inspect PocketBase container %q: %w", containerName, err)
	}
	if container.NetworkSettings == nil {
		return "", fmt.Errorf("PocketBase container %q is missing network settings", containerName)
	}

	networkName := projectNetworkName(projectName)
	endpoint := container.NetworkSettings.Networks[networkName]
	if endpoint == nil {
		return "", fmt.Errorf("PocketBase container %q is not attached to network %q", containerName, networkName)
	}

	ipAddress := strings.TrimSpace(endpoint.IPAddress)
	if ipAddress == "" {
		return "", fmt.Errorf("PocketBase container %q has no IP address on network %q", containerName, networkName)
	}

	return net.JoinHostPort(ipAddress, pocketBaseRuntimePort), nil
}

func (runtime *dockerRuntime) UpsertProjectPocketBaseSuperuser(ctx context.Context, projectName string, email string, password string) error {
	containerName := pocketBaseContainerName(projectName)
	if err := runtime.requireRunningManagedPocketBase(ctx, projectName); err != nil {
		return err
	}

	createResponse, err := runtime.client.ContainerExecCreate(ctx, containerName, dockercontainer.ExecOptions{
		AttachStdout: true,
		AttachStderr: true,
		Cmd: []string{
			"pocketbase",
			"--dir=" + pocketBaseDataMountPath,
			"superuser",
			"upsert",
			email,
			password,
		},
	})
	if err != nil {
		return fmt.Errorf("create PocketBase superuser command: %w", sanitizePocketBaseCredentialError(err, password))
	}

	response, err := runtime.client.ContainerExecAttach(ctx, createResponse.ID, dockercontainer.ExecAttachOptions{})
	if err != nil {
		return fmt.Errorf("run PocketBase superuser command: %w", sanitizePocketBaseCredentialError(err, password))
	}
	defer response.Close()

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, response.Reader); err != nil && err != io.EOF {
		return fmt.Errorf("read PocketBase superuser command output: %w", sanitizePocketBaseCredentialError(err, password))
	}

	inspect, err := runtime.client.ContainerExecInspect(ctx, createResponse.ID)
	if err != nil {
		return fmt.Errorf("inspect PocketBase superuser command: %w", sanitizePocketBaseCredentialError(err, password))
	}
	if inspect.ExitCode != 0 {
		message := strings.TrimSpace(stderr.String())
		if message == "" {
			message = strings.TrimSpace(stdout.String())
		}
		if message == "" {
			message = fmt.Sprintf("exit code %d", inspect.ExitCode)
		}
		return fmt.Errorf("PocketBase superuser command failed: %s", sanitizePocketBaseCredentialText(message, password))
	}

	return nil
}

func (runtime *dockerRuntime) ProjectPocketBaseProxyTarget(ctx context.Context, projectName string) (string, error) {
	if err := runtime.requireRunningManagedPocketBase(ctx, projectName); err != nil {
		return "", err
	}
	if err := runtime.ensureControlPlaneProjectNetworkAttachment(ctx, projectName); err != nil {
		return "", err
	}

	return "http://" + pocketBaseContainerName(projectName) + ":8090", nil
}

func (runtime *dockerRuntime) requireRunningManagedPocketBase(ctx context.Context, projectName string) error {
	containerName := pocketBaseContainerName(projectName)
	container, err := runtime.client.ContainerInspect(ctx, containerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return errProjectRuntimeNotFound
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
	if container.State == nil || !container.State.Running {
		return fmt.Errorf("PocketBase container %q is not running", containerName)
	}

	return nil
}

func (runtime *dockerRuntime) ensureControlPlaneProjectNetworkAttachment(ctx context.Context, projectName string) error {
	if runtime.hostname == nil {
		runtime.hostname = os.Hostname
	}
	containerID, err := runtime.hostname()
	if err != nil {
		return fmt.Errorf("resolve Brain container identity: %w", err)
	}
	containerID = strings.TrimSpace(containerID)
	if containerID == "" {
		return fmt.Errorf("resolve Brain container identity: empty hostname")
	}

	networkName := projectNetworkName(projectName)
	container, err := runtime.client.ContainerInspect(ctx, containerID)
	if err != nil {
		return fmt.Errorf("inspect Brain container %q for PocketBase proxy access: %w", containerID, err)
	}
	if container.NetworkSettings != nil && container.NetworkSettings.Networks[networkName] != nil {
		return nil
	}

	if err := runtime.client.NetworkConnect(ctx, networkName, containerID, &dockernetwork.EndpointSettings{}); err != nil {
		return fmt.Errorf("connect Brain container %q to project network %q: %w", containerID, networkName, err)
	}

	return nil
}

func sanitizePocketBaseCredentialError(err error, password string) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", sanitizePocketBaseCredentialText(err.Error(), password))
}

func sanitizePocketBaseCredentialText(value string, password string) string {
	password = strings.TrimSpace(password)
	if len(password) < minSecretScrubLength {
		return value
	}

	return strings.ReplaceAll(value, password, "[redacted]")
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
