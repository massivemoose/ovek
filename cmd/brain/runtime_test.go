package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockerimage "github.com/docker/docker/api/types/image"
	dockernetwork "github.com/docker/docker/api/types/network"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

func TestProjectResourceNames(t *testing.T) {
	projectName := "demo-app"

	if got := projectNetworkName(projectName); got != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", got)
	}
	if got := pocketBaseContainerName(projectName); got != "ovek-demo-app-pb" {
		t.Fatalf("expected PocketBase container name %q, got %q", "ovek-demo-app-pb", got)
	}
	if got := appContainerName(projectName, "dep-123"); got != "ovek-demo-app-app-dep-123" {
		t.Fatalf("expected app container name %q, got %q", "ovek-demo-app-app-dep-123", got)
	}
}

func TestManagedLabelsIncludesOptionalFields(t *testing.T) {
	labels := managedLabels(managedResourceMetadata{
		ProjectName:  "demo-app",
		Role:         resourceRoleApp,
		DeploymentID: "dep-123",
		JobID:        "job-456",
	})

	want := map[string]string{
		managedLabelKey:    managedLabelValue,
		projectLabelKey:    "demo-app",
		roleLabelKey:       resourceRoleApp,
		deploymentLabelKey: "dep-123",
		jobLabelKey:        "job-456",
	}
	if !reflect.DeepEqual(labels, want) {
		t.Fatalf("expected labels %#v, got %#v", want, labels)
	}
}

func TestDockerRuntimeEnsureProjectNetworkCreatesManagedNetworkWhenMissing(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		networkCreateResponse: dockernetwork.CreateResponse{
			ID: "network-123",
		},
	}
	runtime := newDockerRuntime(client)

	network, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected network creation to succeed, got error: %v", err)
	}

	if network.Name != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", network.Name)
	}
	if network.ID != "network-123" {
		t.Fatalf("expected network ID %q, got %q", "network-123", network.ID)
	}
	if client.networkInspectName != "demo-app-net" {
		t.Fatalf("expected inspect name %q, got %q", "demo-app-net", client.networkInspectName)
	}
	if client.networkCreateName != "demo-app-net" {
		t.Fatalf("expected created network name %q, got %q", "demo-app-net", client.networkCreateName)
	}
	if client.networkCreateOptions.Driver != projectNetworkDriver {
		t.Fatalf("expected network driver %q, got %q", projectNetworkDriver, client.networkCreateOptions.Driver)
	}

	wantLabels := map[string]string{
		managedLabelKey: managedLabelValue,
		projectLabelKey: "demo-app",
		roleLabelKey:    resourceRoleProjectNetwork,
	}
	if !reflect.DeepEqual(client.networkCreateOptions.Labels, wantLabels) {
		t.Fatalf("expected labels %#v, got %#v", wantLabels, client.networkCreateOptions.Labels)
	}
}

func TestDockerRuntimeEnsureProjectNetworkReusesExistingManagedNetwork(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectResponse: dockernetwork.Inspect{
			ID:   "network-123",
			Name: "demo-app-net",
			Labels: map[string]string{
				managedLabelKey: managedLabelValue,
				projectLabelKey: "demo-app",
				roleLabelKey:    resourceRoleProjectNetwork,
			},
		},
	}
	runtime := newDockerRuntime(client)

	network, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected existing managed network to be reused, got error: %v", err)
	}

	if network.ID != "network-123" {
		t.Fatalf("expected network ID %q, got %q", "network-123", network.ID)
	}
	if network.Name != "demo-app-net" {
		t.Fatalf("expected network name %q, got %q", "demo-app-net", network.Name)
	}
	if client.networkCreateCalls != 0 {
		t.Fatalf("expected create not to be called, got %d calls", client.networkCreateCalls)
	}
}

func TestDockerRuntimeEnsureProjectNetworkRejectsUnmanagedExistingNetwork(t *testing.T) {
	client := &fakeDockerClient{
		networkInspectResponse: dockernetwork.Inspect{
			ID:     "network-123",
			Name:   "demo-app-net",
			Labels: map[string]string{},
		},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectNetwork(context.Background(), "demo-app")
	if err == nil {
		t.Fatal("expected unmanaged network to be rejected")
	}
	if !strings.Contains(err.Error(), "already exists but is not managed by ovek") {
		t.Fatalf("expected unmanaged network error, got %v", err)
	}
	if client.networkCreateCalls != 0 {
		t.Fatalf("expected create not to be called, got %d calls", client.networkCreateCalls)
	}
}

func TestPodmanRuntimePullImageUsesNativePuller(t *testing.T) {
	puller := &fakePodmanImagePuller{}
	runtime := &podmanRuntime{
		dockerRuntime:    newDockerRuntime(&fakeDockerClient{}),
		puller:           puller,
		registryInsecure: true,
	}

	err := runtime.PullImage(context.Background(), "localhost:5001/ovek-demo-app:job-123")
	if err != nil {
		t.Fatalf("expected podman image pull to succeed, got error: %v", err)
	}

	if puller.imageRef != "localhost:5001/ovek-demo-app:job-123" {
		t.Fatalf("expected image ref %q, got %q", "localhost:5001/ovek-demo-app:job-123", puller.imageRef)
	}
	if !puller.registryInsecure {
		t.Fatal("expected podman runtime to pass through registryInsecure=true")
	}
}

func TestPodmanServiceImagePullerSetsTLSVerifyFalseForInsecureRegistries(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	puller, err := newPodmanImagePuller(server.URL)
	if err != nil {
		t.Fatalf("expected podman image puller creation to succeed, got error: %v", err)
	}

	err = puller.PullImage(context.Background(), "localhost:5001/ovek-demo-app:job-123", true)
	if err != nil {
		t.Fatalf("expected podman image pull to succeed, got error: %v", err)
	}

	request := <-requests
	if request.Method != http.MethodPost {
		t.Fatalf("expected request method %q, got %q", http.MethodPost, request.Method)
	}
	if request.URL.Path != "/v1.0.0/libpod/images/pull" {
		t.Fatalf("expected request path %q, got %q", "/v1.0.0/libpod/images/pull", request.URL.Path)
	}
	if request.URL.Query().Get("reference") != "localhost:5001/ovek-demo-app:job-123" {
		t.Fatalf("expected image reference query %q, got %q", "localhost:5001/ovek-demo-app:job-123", request.URL.Query().Get("reference"))
	}
	if request.URL.Query().Get("tlsVerify") != "false" {
		t.Fatalf("expected tlsVerify query %q, got %q", "false", request.URL.Query().Get("tlsVerify"))
	}
}

func TestPodmanServiceImagePullerOmitsTLSVerifyForSecureRegistries(t *testing.T) {
	requests := make(chan *http.Request, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests <- r
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("{}"))
	}))
	defer server.Close()

	puller, err := newPodmanImagePuller(server.URL)
	if err != nil {
		t.Fatalf("expected podman image puller creation to succeed, got error: %v", err)
	}

	err = puller.PullImage(context.Background(), "quay.io/podman/hello:latest", false)
	if err != nil {
		t.Fatalf("expected podman image pull to succeed, got error: %v", err)
	}

	request := <-requests
	if request.URL.Query().Get("tlsVerify") != "" {
		t.Fatalf("expected tlsVerify query to be omitted, got %q", request.URL.Query().Get("tlsVerify"))
	}
}

func TestPodmanServiceImagePullerReturnsAPIError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "http: server gave HTTP response to HTTPS client", http.StatusInternalServerError)
	}))
	defer server.Close()

	puller, err := newPodmanImagePuller(server.URL)
	if err != nil {
		t.Fatalf("expected podman image puller creation to succeed, got error: %v", err)
	}

	err = puller.PullImage(context.Background(), "localhost:5001/ovek-demo-app:job-123", true)
	if err == nil {
		t.Fatal("expected podman image pull to fail")
	}
	if !strings.Contains(err.Error(), "http: server gave HTTP response to HTTPS client") {
		t.Fatalf("expected podman pull error to include API response body, got %v", err)
	}
}

type fakeDockerClient struct {
	networkInspectName     string
	networkInspectResponse dockernetwork.Inspect
	networkInspectErr      error
	networkCreateCalls     int
	networkCreateName      string
	networkCreateOptions   dockernetwork.CreateOptions
	networkCreateResponse  dockernetwork.CreateResponse
	networkCreateErr       error
	networkConnectNetwork  string
	networkConnectID       string
	networkConnectConfig   *dockernetwork.EndpointSettings
	networkConnectErr      error
	networkRemoveID        string
	networkRemoveErr       error
	containerListOptions   dockercontainer.ListOptions
	containerListResponse  []dockercontainer.Summary
	containerListErr       error

	containerInspectName            string
	containerInspectCalls           int
	containerInspectResponse        dockercontainer.InspectResponse
	containerInspectErr             error
	containerLogsName               string
	containerLogsOptions            dockercontainer.LogsOptions
	containerLogsResponse           io.ReadCloser
	containerLogsErr                error
	imagePullRef                    string
	imagePullOptions                dockerimage.PullOptions
	imagePullResponse               io.ReadCloser
	imagePullErr                    error
	containerCreateName             string
	containerCreateConfig           *dockercontainer.Config
	containerCreateHostConfig       *dockercontainer.HostConfig
	containerCreateNetworkingConfig *dockernetwork.NetworkingConfig
	containerCreatePlatform         *ocispec.Platform
	containerCreateResponse         dockercontainer.CreateResponse
	containerCreateErr              error
	containerStartID                string
	containerStartOptions           dockercontainer.StartOptions
	containerStartErr               error
	containerStopID                 string
	containerStopOptions            dockercontainer.StopOptions
	containerStopErr                error
	containerRemoveID               string
	containerRemoveOptions          dockercontainer.RemoveOptions
	containerRemoveErr              error
}

type fakePodmanImagePuller struct {
	imageRef         string
	registryInsecure bool
	err              error
}

func (client *fakeDockerClient) NetworkInspect(_ context.Context, networkID string, _ dockernetwork.InspectOptions) (dockernetwork.Inspect, error) {
	client.networkInspectName = networkID
	return client.networkInspectResponse, client.networkInspectErr
}

func (client *fakeDockerClient) NetworkCreate(_ context.Context, name string, options dockernetwork.CreateOptions) (dockernetwork.CreateResponse, error) {
	client.networkCreateCalls++
	client.networkCreateName = name
	client.networkCreateOptions = options
	return client.networkCreateResponse, client.networkCreateErr
}

func (client *fakeDockerClient) NetworkConnect(_ context.Context, networkID string, containerID string, config *dockernetwork.EndpointSettings) error {
	client.networkConnectNetwork = networkID
	client.networkConnectID = containerID
	client.networkConnectConfig = config
	return client.networkConnectErr
}

func (client *fakeDockerClient) NetworkRemove(_ context.Context, networkID string) error {
	client.networkRemoveID = networkID
	return client.networkRemoveErr
}

func (client *fakeDockerClient) ContainerList(_ context.Context, options dockercontainer.ListOptions) ([]dockercontainer.Summary, error) {
	client.containerListOptions = options
	return client.containerListResponse, client.containerListErr
}

func (client *fakeDockerClient) ContainerInspect(_ context.Context, containerID string) (dockercontainer.InspectResponse, error) {
	client.containerInspectName = containerID
	client.containerInspectCalls++
	return client.containerInspectResponse, client.containerInspectErr
}

func (client *fakeDockerClient) ContainerLogs(_ context.Context, container string, options dockercontainer.LogsOptions) (io.ReadCloser, error) {
	client.containerLogsName = container
	client.containerLogsOptions = options
	if client.containerLogsErr != nil {
		return nil, client.containerLogsErr
	}
	if client.containerLogsResponse != nil {
		return client.containerLogsResponse, nil
	}

	return io.NopCloser(strings.NewReader("")), nil
}

func (client *fakeDockerClient) ImagePull(_ context.Context, refStr string, options dockerimage.PullOptions) (io.ReadCloser, error) {
	client.imagePullRef = refStr
	client.imagePullOptions = options
	if client.imagePullErr != nil {
		return nil, client.imagePullErr
	}
	if client.imagePullResponse != nil {
		return client.imagePullResponse, nil
	}

	return io.NopCloser(strings.NewReader("")), nil
}

func (client *fakeDockerClient) ContainerCreate(_ context.Context, config *dockercontainer.Config, hostConfig *dockercontainer.HostConfig, networkingConfig *dockernetwork.NetworkingConfig, platform *ocispec.Platform, containerName string) (dockercontainer.CreateResponse, error) {
	client.containerCreateName = containerName
	client.containerCreateConfig = config
	client.containerCreateHostConfig = hostConfig
	client.containerCreateNetworkingConfig = networkingConfig
	client.containerCreatePlatform = platform
	return client.containerCreateResponse, client.containerCreateErr
}

func (client *fakeDockerClient) ContainerStart(_ context.Context, containerID string, options dockercontainer.StartOptions) error {
	client.containerStartID = containerID
	client.containerStartOptions = options
	return client.containerStartErr
}

func (client *fakeDockerClient) ContainerStop(_ context.Context, containerID string, options dockercontainer.StopOptions) error {
	client.containerStopID = containerID
	client.containerStopOptions = options
	return client.containerStopErr
}

func (client *fakeDockerClient) ContainerRemove(_ context.Context, containerID string, options dockercontainer.RemoveOptions) error {
	client.containerRemoveID = containerID
	client.containerRemoveOptions = options
	return client.containerRemoveErr
}

func (puller *fakePodmanImagePuller) PullImage(_ context.Context, imageRef string, registryInsecure bool) error {
	puller.imageRef = imageRef
	puller.registryInsecure = registryInsecure
	return puller.err
}
