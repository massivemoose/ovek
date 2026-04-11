package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockernetwork "github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const (
	appRuntimePort        = "8080"
	appPortEnv            = "PORT=" + appRuntimePort
	appPocketBaseURL      = "http://db:8090"
	appPocketBaseURLEnv   = "POCKETBASE_URL=" + appPocketBaseURL
	alcesEdgeNetworkName  = "alces-net"
	traefikWebEntrypoint  = "web"
	traefikEnableLabelKey = "traefik.enable"
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

func (runtime *dockerRuntime) EnsureProjectApp(ctx context.Context, job job, imageRef string) (string, error) {
	network, err := runtime.EnsureProjectNetwork(ctx, job.ProjectName)
	if err != nil {
		return "", fmt.Errorf("ensure project network: %w", err)
	}

	spec := newAppContainerSpec(appSpec{
		ProjectName:  job.ProjectName,
		DeploymentID: job.ID,
		JobID:        job.ID,
		ImageRef:     imageRef,
		Network:      network,
	})

	container, err := runtime.client.ContainerInspect(ctx, spec.Name)
	if err == nil {
		if err := validateExistingAppContainer(container, spec); err != nil {
			return "", err
		}
		if err := runtime.ensureAppEdgeNetworkAttachment(ctx, container.ID, container.NetworkSettings, spec); err != nil {
			return "", err
		}
		if container.State != nil && !container.State.Running {
			if err := runtime.client.ContainerStart(ctx, container.ID, dockercontainer.StartOptions{}); err != nil {
				return "", fmt.Errorf("start app container %q: %w", spec.Name, err)
			}
		}

		return container.ID, nil
	}
	if !cerrdefs.IsNotFound(err) {
		return "", fmt.Errorf("inspect app container %q: %w", spec.Name, err)
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
		return "", fmt.Errorf("create app container %q: %w", spec.Name, err)
	}

	if err := runtime.client.NetworkConnect(ctx, alcesEdgeNetworkName, createResponse.ID, spec.EdgeEndpointConfig); err != nil {
		return "", fmt.Errorf("connect app container %q to network %q: %w", spec.Name, alcesEdgeNetworkName, err)
	}
	if err := runtime.client.ContainerStart(ctx, createResponse.ID, dockercontainer.StartOptions{}); err != nil {
		return "", fmt.Errorf("start app container %q: %w", spec.Name, err)
	}

	return createResponse.ID, nil
}

func (runtime *dockerRuntime) WaitForProjectAppReady(ctx context.Context, job job) error {
	readyContext, cancel := context.WithTimeout(ctx, appReadinessTimeout)
	defer cancel()

	address := net.JoinHostPort(appContainerName(job.ProjectName, job.ID), appRuntimePort)
	var lastErr error
	for {
		conn, err := runtime.dialContext(readyContext, "tcp", address)
		if err == nil {
			if closeErr := conn.Close(); closeErr != nil {
				return fmt.Errorf("close readiness probe connection: %w", closeErr)
			}

			return nil
		}

		lastErr = err
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

	return fmt.Errorf("timed out waiting for app container %q to accept TCP connections on %s: %w", appContainerName(job.ProjectName, job.ID), address, lastErr)
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
	if err := requireManagedResourceOwnership(deployment.AppContainerName, container.Config.Labels, managedResourceMetadata{
		ProjectName:  deployment.ProjectName,
		Role:         resourceRoleApp,
		DeploymentID: deployment.ID,
	}); err != nil {
		return err
	}

	return runtime.removeManagedContainer(ctx, deployment.AppContainerName, container, "app container")
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

	routerName := appRouterName(spec.ProjectName, spec.DeploymentID)
	serviceName := appServiceName(spec.ProjectName, spec.DeploymentID)
	labels := managedLabels(metadata)
	labels[traefikEnableLabelKey] = "true"
	labels["traefik.http.routers."+routerName+".entrypoints"] = traefikWebEntrypoint
	labels["traefik.http.routers."+routerName+".rule"] = fmt.Sprintf("Host(`%s.localhost`)", spec.ProjectName)
	labels["traefik.http.routers."+routerName+".service"] = serviceName
	labels["traefik.http.services."+serviceName+".loadbalancer.server.port"] = appRuntimePort
	labels["traefik.docker.network"] = alcesEdgeNetworkName

	return appContainerSpec{
		Name:     appContainerName(spec.ProjectName, spec.DeploymentID),
		Metadata: metadata,
		Config: &dockercontainer.Config{
			Image:  spec.ImageRef,
			Env:    []string{appPortEnv, appPocketBaseURLEnv},
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

func (runtime *dockerRuntime) ensureAppEdgeNetworkAttachment(ctx context.Context, containerID string, networkSettings *dockercontainer.NetworkSettings, spec appContainerSpec) error {
	if networkSettings == nil {
		return fmt.Errorf("app container %q is missing network settings", spec.Name)
	}
	if networkSettings.Networks[alcesEdgeNetworkName] != nil {
		return nil
	}

	if err := runtime.client.NetworkConnect(ctx, alcesEdgeNetworkName, containerID, spec.EdgeEndpointConfig); err != nil {
		return fmt.Errorf("connect app container %q to network %q: %w", spec.Name, alcesEdgeNetworkName, err)
	}

	return nil
}

func appRouterName(projectName string, deploymentID string) string {
	return "app-" + projectName + "-" + deploymentID
}

func appServiceName(projectName string, deploymentID string) string {
	return "app-" + projectName + "-" + deploymentID
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
