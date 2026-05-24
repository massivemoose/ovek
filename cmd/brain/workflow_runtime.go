package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/filters"
	dockernetwork "github.com/docker/docker/api/types/network"
	"github.com/docker/docker/pkg/stdcopy"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

const workflowStopTimeoutSeconds = 10

type workflowContainerSpec struct {
	Name             string
	Config           *dockercontainer.Config
	HostConfig       *dockercontainer.HostConfig
	NetworkingConfig *dockernetwork.NetworkingConfig
}

func workflowContainerName(projectName string, workflowName string, runID string) string {
	return "ovek-" + projectName + "-wf-" + runID
}

func workflowContainerEnv(run workflowRun, env []string) []string {
	builtins := []string{
		appPortEnv,
		appPocketBaseURLEnv,
		"OVEK_PROJECT=" + run.ProjectName,
		"OVEK_WORKFLOW=" + run.WorkflowName,
		"OVEK_WORKFLOW_RUN_ID=" + run.ID,
	}
	return append(builtins, env...)
}

func newWorkflowContainerSpec(run workflowRun, imageRef string, network projectNetwork, env []string) workflowContainerSpec {
	networkName := network.Name
	labels := managedLabels(managedResourceMetadata{
		ProjectName:   run.ProjectName,
		Role:          resourceRoleWorkflow,
		WorkflowName:  run.WorkflowName,
		WorkflowRunID: run.ID,
	})

	return workflowContainerSpec{
		Name: workflowContainerName(run.ProjectName, run.WorkflowName, run.ID),
		Config: &dockercontainer.Config{
			Image:  imageRef,
			Env:    workflowContainerEnv(run, env),
			Labels: labels,
		},
		HostConfig: &dockercontainer.HostConfig{
			NetworkMode: dockercontainer.NetworkMode(networkName),
			RestartPolicy: dockercontainer.RestartPolicy{
				Name: dockercontainer.RestartPolicyDisabled,
			},
		},
		NetworkingConfig: &dockernetwork.NetworkingConfig{
			EndpointsConfig: map[string]*dockernetwork.EndpointSettings{
				networkName: {},
			},
		},
	}
}

func (runtime *dockerRuntime) CreateWorkflowContainer(ctx context.Context, run workflowRun, imageRef string, env []string) (string, error) {
	network, err := runtime.EnsureProjectNetwork(ctx, run.ProjectName)
	if err != nil {
		return "", fmt.Errorf("ensure project network: %w", err)
	}

	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		imageRef = run.RuntimeImageID
	}
	if imageRef == "" {
		imageRef = run.SourceImageRef
	}
	spec := newWorkflowContainerSpec(run, imageRef, network, env)

	createResponse, err := runtime.client.ContainerCreate(
		ctx,
		spec.Config,
		spec.HostConfig,
		spec.NetworkingConfig,
		(*ocispec.Platform)(nil),
		spec.Name,
	)
	if err != nil {
		return "", fmt.Errorf("create workflow container %q: %w", spec.Name, err)
	}

	return createResponse.ID, nil
}

func (runtime *dockerRuntime) StartWorkflowContainer(ctx context.Context, containerID string) error {
	if err := runtime.client.ContainerStart(ctx, containerID, dockercontainer.StartOptions{}); err != nil {
		return fmt.Errorf("start workflow container %q: %w", containerID, err)
	}
	return nil
}

func (runtime *dockerRuntime) WaitWorkflowContainer(ctx context.Context, containerID string) (int, error) {
	waitCh, errCh := runtime.client.ContainerWait(ctx, containerID, dockercontainer.WaitConditionNotRunning)
	select {
	case <-ctx.Done():
		return 0, ctx.Err()
	case err := <-errCh:
		if err != nil {
			return 0, fmt.Errorf("wait for workflow container %q: %w", containerID, err)
		}
		return 0, errors.New("workflow container wait ended without status")
	case response := <-waitCh:
		if response.Error != nil && strings.TrimSpace(response.Error.Message) != "" {
			return int(response.StatusCode), fmt.Errorf("workflow container %q wait error: %s", containerID, response.Error.Message)
		}
		return int(response.StatusCode), nil
	}
}

func (runtime *dockerRuntime) StreamWorkflowContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error) {
	logs, err := runtime.client.ContainerLogs(ctx, containerID, dockercontainer.LogsOptions{
		ShowStdout: true,
		ShowStderr: true,
		Follow:     true,
		Tail:       "all",
	})
	if err != nil {
		return nil, fmt.Errorf("stream workflow container %q logs: %w", containerID, err)
	}

	reader, writer := io.Pipe()
	go func() {
		defer logs.Close()

		_, copyErr := stdcopy.StdCopy(writer, writer, logs)
		_ = writer.CloseWithError(copyErr)
	}()

	return reader, nil
}

func (runtime *dockerRuntime) StopWorkflowContainer(ctx context.Context, containerID string) error {
	timeout := workflowStopTimeoutSeconds
	if err := runtime.client.ContainerStop(ctx, containerID, dockercontainer.StopOptions{Timeout: &timeout}); err != nil {
		return fmt.Errorf("stop workflow container %q: %w", containerID, err)
	}
	return nil
}

func (runtime *dockerRuntime) RemoveWorkflowContainer(ctx context.Context, containerID string) error {
	if err := runtime.client.ContainerRemove(ctx, containerID, dockercontainer.RemoveOptions{Force: true}); err != nil {
		return fmt.Errorf("remove workflow container %q: %w", containerID, err)
	}
	return nil
}

func (runtime *dockerRuntime) RemoveProjectWorkflowContainers(ctx context.Context, projectName string) error {
	containers, err := runtime.client.ContainerList(ctx, dockercontainer.ListOptions{
		All: true,
		Filters: filters.NewArgs(
			filters.Arg("label", managedLabelKey+"="+managedLabelValue),
			filters.Arg("label", projectLabelKey+"="+projectName),
			filters.Arg("label", roleLabelKey+"="+resourceRoleWorkflow),
		),
	})
	if err != nil {
		return fmt.Errorf("list workflow containers for project %q: %w", projectName, err)
	}

	for _, container := range containers {
		if err := runtime.client.ContainerRemove(ctx, container.ID, dockercontainer.RemoveOptions{Force: true}); err != nil {
			if cerrdefs.IsNotFound(err) {
				continue
			}
			return fmt.Errorf("remove workflow container %q: %w", container.ID, err)
		}
	}
	return nil
}
