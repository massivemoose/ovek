package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	cerrdefs "github.com/containerd/errdefs"
	dockernetwork "github.com/docker/docker/api/types/network"
)

type projectRuntimeService interface {
	GetRuntime(ctx context.Context, projectName string) (projectRuntimeView, error)
	ReadRuntimeLogs(ctx context.Context, projectName string) (io.ReadCloser, error)
	StreamRuntimeLogs(ctx context.Context, projectName string) (io.ReadCloser, error)
}

type projectRuntimeReader interface {
	ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error)
	GetProjectPocketBaseRuntime(ctx context.Context, projectName string) (projectRuntimeContainer, bool, error)
	GetProjectNetworkRuntime(ctx context.Context, projectName string) (projectRuntimeNetwork, bool, error)
	ReadProjectAppLogs(ctx context.Context, deployment deploymentRecord, options runtimeLogOptions) (io.ReadCloser, error)
}

type managedProjectRuntimeService struct {
	db      *sql.DB
	runtime projectRuntimeReader
}

func newManagedProjectRuntimeService(db *sql.DB, runtime projectRuntimeReader) managedProjectRuntimeService {
	return managedProjectRuntimeService{
		db:      db,
		runtime: runtime,
	}
}

func handleGetProjectRuntime(service projectRuntimeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		runtimeView, err := service.GetRuntime(r.Context(), projectName)
		if errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchProjectRuntimeFailed, "failed to fetch project runtime")
			return
		}

		writeJSON(w, http.StatusOK, runtimeView)
	}
}

func handleGetProjectRuntimeLogs(service projectRuntimeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		logs, err := service.ReadRuntimeLogs(r.Context(), projectName)
		if errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if errors.Is(err, errProjectRuntimeNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectRuntimeNotFound, "project runtime not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
			return
		}
		defer logs.Close()

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, logs)
	}
}

func handleGetProjectRuntimeLogsStream(service projectRuntimeService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		logs, err := service.StreamRuntimeLogs(r.Context(), projectName)
		if errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if errors.Is(err, errProjectRuntimeNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectRuntimeNotFound, "project runtime not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
			return
		}
		defer logs.Close()

		flusher, ok := w.(http.Flusher)
		if !ok {
			writeJSONError(w, http.StatusInternalServerError, errorCodeFetchRuntimeLogsFailed, "failed to fetch runtime logs")
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)

		if err := streamSSELogReader(r.Context(), w, flusher, logs); err != nil {
			_ = writeSSEEvent(w, "error", "failed to fetch runtime logs")
			flusher.Flush()
		}
	}
}

func (service managedProjectRuntimeService) GetRuntime(ctx context.Context, projectName string) (projectRuntimeView, error) {
	project, err := getProject(service.db, projectName)
	if errors.Is(err, sql.ErrNoRows) {
		return projectRuntimeView{}, errProjectNotFound
	}
	if err != nil {
		return projectRuntimeView{}, fmt.Errorf("get project %q: %w", projectName, err)
	}

	runtimeView := projectRuntimeView{
		ProjectName:         project.Name,
		CurrentDeploymentID: project.CurrentDeploymentID,
	}

	apps, err := service.runtime.ListProjectApps(ctx, projectName)
	if err != nil {
		return projectRuntimeView{}, fmt.Errorf("list project apps: %w", err)
	}

	currentDeployment, hasCurrentDeployment, err := getProjectCurrentDeployment(service.db, projectName)
	if err != nil {
		return projectRuntimeView{}, err
	}
	if hasCurrentDeployment {
		runtimeView.App = &projectRuntimeApp{
			ContainerName: currentDeployment.AppContainerName,
			ImageRef:      currentDeployment.ImageRef,
			Running:       false,
		}

		if app, found := findProjectAppByDeploymentID(apps, currentDeployment.ID); found {
			runtimeView.App = &projectRuntimeApp{
				ContainerName: app.AppContainerName,
				ImageRef:      app.ImageRef,
				Running:       app.Running,
			}
		}
	}

	pocketBase, found, err := service.runtime.GetProjectPocketBaseRuntime(ctx, projectName)
	if err != nil {
		return projectRuntimeView{}, fmt.Errorf("get PocketBase runtime: %w", err)
	}
	if found {
		runtimeView.PocketBase = &pocketBase
	}

	network, found, err := service.runtime.GetProjectNetworkRuntime(ctx, projectName)
	if err != nil {
		return projectRuntimeView{}, fmt.Errorf("get project network runtime: %w", err)
	}
	if found {
		runtimeView.Network = &network
	}

	return runtimeView, nil
}

var errProjectRuntimeNotFound = errors.New("project runtime not found")

func (service managedProjectRuntimeService) ReadRuntimeLogs(ctx context.Context, projectName string) (io.ReadCloser, error) {
	return service.openRuntimeLogs(ctx, projectName, runtimeLogOptions{})
}

func (service managedProjectRuntimeService) StreamRuntimeLogs(ctx context.Context, projectName string) (io.ReadCloser, error) {
	return service.openRuntimeLogs(ctx, projectName, runtimeLogOptions{Follow: true})
}

func (service managedProjectRuntimeService) openRuntimeLogs(ctx context.Context, projectName string, options runtimeLogOptions) (io.ReadCloser, error) {
	project, err := getProject(service.db, projectName)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, errProjectNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get project %q: %w", projectName, err)
	}

	if project.CurrentDeploymentID == nil || strings.TrimSpace(*project.CurrentDeploymentID) == "" {
		return nil, errProjectRuntimeNotFound
	}

	currentDeployment, found, err := getProjectCurrentDeployment(service.db, projectName)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, errProjectRuntimeNotFound
	}

	logs, err := service.runtime.ReadProjectAppLogs(ctx, currentDeployment, options)
	if err != nil {
		return nil, fmt.Errorf("read runtime logs for project %q: %w", projectName, err)
	}

	return logs, nil
}

func (runtime *dockerRuntime) GetProjectPocketBaseRuntime(ctx context.Context, projectName string) (projectRuntimeContainer, bool, error) {
	containerName := pocketBaseContainerName(projectName)
	container, err := runtime.client.ContainerInspect(ctx, containerName)
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return projectRuntimeContainer{}, false, nil
		}

		return projectRuntimeContainer{}, false, fmt.Errorf("inspect PocketBase container %q: %w", containerName, err)
	}
	if container.Config == nil {
		return projectRuntimeContainer{}, false, fmt.Errorf("PocketBase container %q is missing config", containerName)
	}
	if err := requireManagedResourceOwnership(containerName, container.Config.Labels, managedResourceMetadata{
		ProjectName: projectName,
		Role:        resourceRolePocketBase,
	}); err != nil {
		return projectRuntimeContainer{}, false, err
	}

	return projectRuntimeContainer{
		ContainerName: containerName,
		Running:       container.State != nil && container.State.Running,
	}, true, nil
}

func (runtime *dockerRuntime) GetProjectNetworkRuntime(ctx context.Context, projectName string) (projectRuntimeNetwork, bool, error) {
	networkName := projectNetworkName(projectName)
	network, err := runtime.client.NetworkInspect(ctx, networkName, dockernetwork.InspectOptions{})
	if err != nil {
		if cerrdefs.IsNotFound(err) {
			return projectRuntimeNetwork{}, false, nil
		}

		return projectRuntimeNetwork{}, false, fmt.Errorf("inspect project network %q: %w", networkName, err)
	}
	if err := requireManagedResourceOwnership(networkName, network.Labels, managedResourceMetadata{
		ProjectName: projectName,
		Role:        resourceRoleProjectNetwork,
	}); err != nil {
		return projectRuntimeNetwork{}, false, err
	}

	if network.Name == "" {
		network.Name = networkName
	}

	return projectRuntimeNetwork{Name: network.Name}, true, nil
}
