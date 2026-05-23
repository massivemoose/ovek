package main

import (
	"context"
	"io"
)

const (
	runtimeEnginePodman = "podman"
)

type runtimeLogOptions struct {
	Follow bool
}

type appReadinessTarget struct {
	Address string
	URL     string
}

type workflowImageMetadata struct {
	SourceImageRef     string
	ResolvedRepoDigest string
	RuntimeImageID     string
}

type workflowImageResolver interface {
	ResolveWorkflowImage(ctx context.Context, imageRef string) (workflowImageMetadata, error)
}

type Runtime interface {
	PullImage(ctx context.Context, imageRef string) error
	workflowImageResolver
	EnsureProjectNetwork(ctx context.Context, projectName string) (projectNetwork, error)
	RemoveProjectNetwork(ctx context.Context, projectName string) error
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	RemoveProjectPocketBase(ctx context.Context, projectName string) error
	WaitForProjectPocketBaseReady(ctx context.Context, projectName string) error
	UpsertProjectPocketBaseSuperuser(ctx context.Context, projectName string, email string, password string) error
	ProjectPocketBaseProxyTarget(ctx context.Context, projectName string) (string, error)
	EnsureProjectApp(ctx context.Context, job job, imageRef string, env []string) (string, error)
	WaitForProjectAppReady(ctx context.Context, job job) error
	ResolveProjectAppReadinessTarget(ctx context.Context, projectName string, deploymentID string) (appReadinessTarget, error)
	StartProjectApp(ctx context.Context, deployment deploymentRecord) error
	StopProjectApp(ctx context.Context, deployment deploymentRecord) error
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
	ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error)
	GetProjectPocketBaseRuntime(ctx context.Context, projectName string) (projectRuntimeContainer, bool, error)
	GetProjectNetworkRuntime(ctx context.Context, projectName string) (projectRuntimeNetwork, bool, error)
	ReadProjectAppLogs(ctx context.Context, deployment deploymentRecord, options runtimeLogOptions) (io.ReadCloser, error)
	CreateWorkflowContainer(ctx context.Context, run workflowRun, imageRef string, env []string) (string, error)
	StartWorkflowContainer(ctx context.Context, containerID string) error
	WaitWorkflowContainer(ctx context.Context, containerID string) (int, error)
	StreamWorkflowContainerLogs(ctx context.Context, containerID string) (io.ReadCloser, error)
	StopWorkflowContainer(ctx context.Context, containerID string) error
	RemoveWorkflowContainer(ctx context.Context, containerID string) error
}

var _ Runtime = (*podmanRuntime)(nil)
