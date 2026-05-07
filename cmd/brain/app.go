package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	appRuntimePort        = "8080"
	appPortEnv            = "PORT=" + appRuntimePort
	appPocketBaseURL      = "http://db:8090"
	appPocketBaseURLEnv   = "POCKETBASE_URL=" + appPocketBaseURL
	ovekEdgeNetworkName   = "ovek-net"
	appReadinessTimeout   = 20 * time.Second
	appReadinessInterval  = 250 * time.Millisecond
	appReadinessDialTime  = 1 * time.Second
	appStopTimeoutSeconds = 10
)

type dialContextFunc func(ctx context.Context, network string, address string) (net.Conn, error)
type sleepFunc func(ctx context.Context, delay time.Duration) error

type appSpec struct {
	ProjectName  string
	DeploymentID string
	JobID        string
	ImageRef     string
	Network      projectNetwork
	Env          []string
}

type appContainerSpec struct {
	Name               string
	Metadata           managedResourceMetadata
	Config             *dockercontainer.Config
	HostConfig         *dockercontainer.HostConfig
	NetworkingConfig   *dockernetwork.NetworkingConfig
	EdgeEndpointConfig *dockernetwork.EndpointSettings
	ProjectNetworkName string
}

type projectAppRuntime struct {
	DeploymentID            string
	ProjectName             string
	AppContainerName        string
	ImageRef                string
	NetworkName             string
	PocketBaseContainerName string
	CreatedAt               string
	Running                 bool
}

func (runtime *dockerRuntime) EnsureProjectApp(ctx context.Context, job job, imageRef string, env []string) (string, error) {
	return ensureProjectApp(ctx, runtime, runtime, job, imageRef, env)
}

func (runtime *podmanRuntime) EnsureProjectApp(ctx context.Context, job job, imageRef string, env []string) (string, error) {
	return ensureProjectApp(ctx, runtime, runtime.dockerRuntime, job, imageRef, env)
}

func ensureProjectApp(ctx context.Context, imageRuntime Runtime, containerRuntime *dockerRuntime, job job, imageRef string, env []string) (string, error) {
	network, err := containerRuntime.EnsureProjectNetwork(ctx, job.ProjectName)
	if err != nil {
		return "", fmt.Errorf("ensure project network: %w", err)
	}

	spec := newAppContainerSpec(appSpec{
		ProjectName:  job.ProjectName,
		DeploymentID: job.ID,
		JobID:        job.ID,
		ImageRef:     imageRef,
		Network:      network,
		Env:          env,
	})

	container, err := containerRuntime.client.ContainerInspect(ctx, spec.Name)
	if err == nil {
		if err := validateExistingAppContainer(container, spec); err != nil {
			return "", err
		}
		if err := containerRuntime.ensureAppEdgeNetworkAttachment(ctx, container.ID, container.NetworkSettings, spec); err != nil {
			return "", err
		}
		if container.State != nil && !container.State.Running {
			if err := containerRuntime.client.ContainerStart(ctx, container.ID, dockercontainer.StartOptions{}); err != nil {
				return "", fmt.Errorf("start app container %q: %w", spec.Name, err)
			}
		}

		return container.ID, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspect app container %q: %w", spec.Name, err)
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
		return "", fmt.Errorf("create app container %q: %w", spec.Name, err)
	}

	if err := containerRuntime.client.NetworkConnect(ctx, ovekEdgeNetworkName, createResponse.ID, spec.EdgeEndpointConfig); err != nil {
		return "", fmt.Errorf("connect app container %q to network %q: %w", spec.Name, ovekEdgeNetworkName, err)
	}
	if err := containerRuntime.client.ContainerStart(ctx, createResponse.ID, dockercontainer.StartOptions{}); err != nil {
		return "", fmt.Errorf("start app container %q: %w", spec.Name, err)
	}

	return createResponse.ID, nil
}

func (runtime *dockerRuntime) WaitForProjectAppReady(ctx context.Context, job job) error {
	readyContext, cancel := context.WithTimeout(ctx, appReadinessTimeout)
	defer cancel()

	address := ""
	var lastErr error
	for {
		target, err := runtime.ResolveProjectAppReadinessTarget(readyContext, job.ProjectName, job.ID)
		if err == nil {
			address = target.Address
		}
		lastErr = err
		if lastErr == nil {
			conn, err := runtime.dialContext(readyContext, "tcp", address)
			if err == nil {
				if closeErr := conn.Close(); closeErr != nil {
					return fmt.Errorf("close readiness probe connection: %w", closeErr)
				}

				return nil
			}

			lastErr = err
		}

		if errors.Is(readyContext.Err(), context.DeadlineExceeded) {
			break
		}

		if waitErr := runtime.sleep(readyContext, appReadinessInterval); waitErr != nil {
			if errors.Is(waitErr, context.DeadlineExceeded) || errors.Is(waitErr, context.Canceled) {
				break
			}

			return fmt.Errorf("wait for next readiness probe: %w", waitErr)
		}
	}

	if lastErr == nil {
		lastErr = readyContext.Err()
	}

	containerName := appContainerName(job.ProjectName, job.ID)
	return fmt.Errorf("timed out waiting for app container %q to accept TCP connections on %s: %w", containerName, address, lastErr)
}

func (runtime *dockerRuntime) ResolveProjectAppReadinessTarget(ctx context.Context, projectName string, deploymentID string) (appReadinessTarget, error) {
	containerName := appContainerName(projectName, deploymentID)
	container, err := runtime.client.ContainerInspect(ctx, containerName)
	if err != nil {
		return appReadinessTarget{}, fmt.Errorf("inspect app container %q: %w", containerName, err)
	}

	address, err := readinessAddressForAppContainer(container, containerName)
	if err != nil {
		return appReadinessTarget{}, err
	}

	return appReadinessTarget{
		Address: address,
		URL:     "http://" + address,
	}, nil
}

func readinessAddressForAppContainer(container dockercontainer.InspectResponse, containerName string) (string, error) {
	if container.NetworkSettings == nil {
		return "", fmt.Errorf("app container %q is missing network settings", containerName)
	}

	endpoint := container.NetworkSettings.Networks[ovekEdgeNetworkName]
	if endpoint == nil {
		return "", fmt.Errorf("app container %q is not attached to network %q", containerName, ovekEdgeNetworkName)
	}

	ipAddress := strings.TrimSpace(endpoint.IPAddress)
	if ipAddress == "" {
		return "", fmt.Errorf("app container %q has no IP address on network %q", containerName, ovekEdgeNetworkName)
	}

	return net.JoinHostPort(ipAddress, appRuntimePort), nil
}

func (runtime *dockerRuntime) RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error {
	container, err := runtime.client.ContainerInspect(ctx, deployment.AppContainerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return nil
		}

		return fmt.Errorf("inspect app container %q: %w", deployment.AppContainerName, err)
	}
	if container.Config == nil {
		return fmt.Errorf("app container %q is missing config", deployment.AppContainerName)
	}
	if err := validateManagedProjectAppContainer(deployment, container); err != nil {
		return err
	}

	return runtime.removeManagedContainer(ctx, deployment.AppContainerName, container, "app container")
}

func (runtime *dockerRuntime) StartProjectApp(ctx context.Context, deployment deploymentRecord) error {
	container, err := runtime.client.ContainerInspect(ctx, deployment.AppContainerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return errProjectRuntimeNotFound
		}
		return fmt.Errorf("inspect app container %q: %w", deployment.AppContainerName, err)
	}
	if container.Config == nil {
		return fmt.Errorf("app container %q is missing config", deployment.AppContainerName)
	}
	if err := validateManagedProjectAppContainer(deployment, container); err != nil {
		return err
	}
	if container.State != nil && container.State.Running {
		return nil
	}

	containerID := container.ID
	if containerID == "" {
		containerID = deployment.AppContainerName
	}
	if err := runtime.client.ContainerStart(ctx, containerID, dockercontainer.StartOptions{}); err != nil {
		return fmt.Errorf("start app container %q: %w", deployment.AppContainerName, err)
	}
	return nil
}

func (runtime *podmanRuntime) StartProjectApp(ctx context.Context, deployment deploymentRecord) error {
	return runtime.dockerRuntime.StartProjectApp(ctx, deployment)
}

func (runtime *dockerRuntime) StopProjectApp(ctx context.Context, deployment deploymentRecord) error {
	container, err := runtime.client.ContainerInspect(ctx, deployment.AppContainerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return errProjectRuntimeNotFound
		}
		return fmt.Errorf("inspect app container %q: %w", deployment.AppContainerName, err)
	}
	if container.Config == nil {
		return fmt.Errorf("app container %q is missing config", deployment.AppContainerName)
	}
	if err := validateManagedProjectAppContainer(deployment, container); err != nil {
		return err
	}
	if container.State == nil || !container.State.Running {
		return nil
	}

	containerID := container.ID
	if containerID == "" {
		containerID = deployment.AppContainerName
	}
	timeout := appStopTimeoutSeconds
	if err := runtime.client.ContainerStop(ctx, containerID, dockercontainer.StopOptions{Timeout: &timeout}); err != nil && !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("stop app container %q: %w", deployment.AppContainerName, err)
	}
	return nil
}

func (runtime *podmanRuntime) StopProjectApp(ctx context.Context, deployment deploymentRecord) error {
	return runtime.dockerRuntime.StopProjectApp(ctx, deployment)
}

func (runtime *dockerRuntime) ReadProjectAppLogs(ctx context.Context, deployment deploymentRecord, options runtimeLogOptions) (io.ReadCloser, error) {
	container, err := runtime.client.ContainerInspect(ctx, deployment.AppContainerName)
	if err != nil {
		return nil, fmt.Errorf("inspect app container %q: %w", deployment.AppContainerName, err)
	}
	if container.Config == nil {
		return nil, fmt.Errorf("app container %q is missing config", deployment.AppContainerName)
	}
	if err := validateManagedProjectAppContainer(deployment, container); err != nil {
		return nil, err
	}

	logs, err := runtime.client.ContainerLogs(ctx, deployment.AppContainerName, dockercontainer.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     options.Follow,
		Tail:       "all",
	})
	if err != nil {
		return nil, fmt.Errorf("read app container %q logs: %w", deployment.AppContainerName, err)
	}

	reader, writer := io.Pipe()
	go func() {
		defer logs.Close()

		_, copyErr := stdcopy.StdCopy(writer, writer, logs)
		_ = writer.CloseWithError(copyErr)
	}()

	return reader, nil
}

func (runtime *dockerRuntime) ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error) {
	containers, err := runtime.client.ContainerList(ctx, dockercontainer.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", managedLabelKey+"="+managedLabelValue),
			filters.Arg("label", projectLabelKey+"="+projectName),
			filters.Arg("label", roleLabelKey+"="+resourceRoleApp),
		),
	})
	if err != nil {
		return nil, fmt.Errorf("list project app containers: %w", err)
	}

	apps := make([]projectAppRuntime, 0, len(containers))
	for _, container := range containers {
		deploymentID := container.Labels[deploymentLabelKey]
		if deploymentID == "" {
			name := container.ID
			if len(container.Names) > 0 {
				name = strings.TrimPrefix(container.Names[0], "/")
			}

			return nil, fmt.Errorf("managed app container %q is missing deployment label", name)
		}

		appName := container.ID
		if len(container.Names) > 0 {
			appName = strings.TrimPrefix(container.Names[0], "/")
		}

		apps = append(apps, projectAppRuntime{
			DeploymentID:            deploymentID,
			ProjectName:             projectName,
			AppContainerName:        appName,
			ImageRef:                container.Image,
			NetworkName:             projectNetworkName(projectName),
			PocketBaseContainerName: pocketBaseContainerName(projectName),
			CreatedAt:               time.Unix(container.Created, 0).UTC().Format(time.RFC3339Nano),
			Running:                 container.State == "running",
		})
	}

	return apps, nil
}

func newAppContainerSpec(spec appSpec) appContainerSpec {
	metadata := managedResourceMetadata{
		ProjectName:  spec.ProjectName,
		Role:         resourceRoleApp,
		DeploymentID: spec.DeploymentID,
		JobID:        spec.JobID,
	}

	labels := managedLabels(metadata)

	return appContainerSpec{
		Name:     appContainerName(spec.ProjectName, spec.DeploymentID),
		Metadata: metadata,
		Config: &dockercontainer.Config{
			Image:  spec.ImageRef,
			Env:    appEnvironment(spec.Env),
			Labels: labels,
		},
		HostConfig: &dockercontainer.HostConfig{
			NetworkMode: dockercontainer.NetworkMode(spec.Network.Name),
			RestartPolicy: dockercontainer.RestartPolicy{
				Name: dockercontainer.RestartPolicyUnlessStopped,
			},
		},
		NetworkingConfig: &dockernetwork.NetworkingConfig{
			EndpointsConfig: map[string]*dockernetwork.EndpointSettings{
				spec.Network.Name: {},
			},
		},
		EdgeEndpointConfig: &dockernetwork.EndpointSettings{},
		ProjectNetworkName: spec.Network.Name,
	}
}

func appEnvironment(projectEnv []string) []string {
	env := []string{appPortEnv, appPocketBaseURLEnv}
	env = append(env, projectEnv...)
	return env
}

func validateExistingAppContainer(container dockercontainer.InspectResponse, spec appContainerSpec) error {
	if container.Config == nil {
		return fmt.Errorf("app container %q is missing config", spec.Name)
	}
	if err := requireManagedResourceOwnership(spec.Name, container.Config.Labels, spec.Metadata); err != nil {
		return err
	}
	if container.Config.Image != spec.Config.Image {
		return fmt.Errorf("app container %q already exists with image %q, not %q", spec.Name, container.Config.Image, spec.Config.Image)
	}
	for _, env := range spec.Config.Env {
		if !slices.Contains(container.Config.Env, env) {
			return fmt.Errorf("app container %q is missing env %q", spec.Name, env)
		}
	}
	for key, want := range spec.Config.Labels {
		if got := container.Config.Labels[key]; got != want {
			return fmt.Errorf("app container %q has label %q=%q, not %q", spec.Name, key, got, want)
		}
	}
	if container.NetworkSettings == nil {
		return fmt.Errorf("app container %q is missing network settings", spec.Name)
	}
	if container.NetworkSettings.Networks[spec.ProjectNetworkName] == nil {
		return fmt.Errorf("app container %q is not attached to network %q", spec.Name, spec.ProjectNetworkName)
	}

	return nil
}

func validateManagedProjectAppContainer(deployment deploymentRecord, container dockercontainer.InspectResponse) error {
	return requireManagedResourceOwnership(deployment.AppContainerName, container.Config.Labels, managedResourceMetadata{
		ProjectName:  deployment.ProjectName,
		Role:         resourceRoleApp,
		DeploymentID: deployment.ID,
	})
}

func (runtime *dockerRuntime) ensureAppEdgeNetworkAttachment(ctx context.Context, containerID string, networkSettings *dockercontainer.NetworkSettings, spec appContainerSpec) error {
	if networkSettings == nil {
		return fmt.Errorf("app container %q is missing network settings", spec.Name)
	}
	if networkSettings.Networks[ovekEdgeNetworkName] != nil {
		return nil
	}

	if err := runtime.client.NetworkConnect(ctx, ovekEdgeNetworkName, containerID, spec.EdgeEndpointConfig); err != nil {
		return fmt.Errorf("connect app container %q to network %q: %w", spec.Name, ovekEdgeNetworkName, err)
	}

	return nil
}

func sleepWithContext(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
