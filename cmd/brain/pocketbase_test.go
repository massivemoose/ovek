package main

import (
	"context"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cerrdefs "github.com/containerd/errdefs"
	dockercontainer "github.com/docker/docker/api/types/container"
	dockermount "github.com/docker/docker/api/types/mount"
	dockernetwork "github.com/docker/docker/api/types/network"
)

func TestPocketBaseDataDir(t *testing.T) {
	got := pocketBaseDataDir("/srv/ovek/projects", "demo-app")
	want := filepath.Join("/srv/ovek/projects", "demo-app", pocketBaseDataDirName)
	if got != want {
		t.Fatalf("expected PocketBase data dir %q, got %q", want, got)
	}
}

func TestNewPocketBaseContainerSpec(t *testing.T) {
	spec := newPocketBaseContainerSpec(pocketBaseSpec{
		ProjectName:         "demo-app",
		Image:               defaultPocketBaseImage,
		ProjectsHostDataDir: "/srv/ovek/projects",
		Network: projectNetwork{
			ID:   "network-123",
			Name: "demo-app-net",
		},
	})

	if spec.Name != "ovek-demo-app-pb" {
		t.Fatalf("expected container name %q, got %q", "ovek-demo-app-pb", spec.Name)
	}
	if spec.HostDataDir != filepath.Join("/srv/ovek/projects", "demo-app", pocketBaseDataDirName) {
		t.Fatalf("expected host data dir %q, got %q", filepath.Join("/srv/ovek/projects", "demo-app", pocketBaseDataDirName), spec.HostDataDir)
	}
	if spec.Config.Image != defaultPocketBaseImage {
		t.Fatalf("expected image %q, got %q", defaultPocketBaseImage, spec.Config.Image)
	}

	wantLabels := map[string]string{
		managedLabelKey: managedLabelValue,
		projectLabelKey: "demo-app",
		roleLabelKey:    resourceRolePocketBase,
	}
	if !reflect.DeepEqual(spec.Config.Labels, wantLabels) {
		t.Fatalf("expected labels %#v, got %#v", wantLabels, spec.Config.Labels)
	}

	if spec.HostConfig.NetworkMode != dockercontainer.NetworkMode("demo-app-net") {
		t.Fatalf("expected network mode %q, got %q", dockercontainer.NetworkMode("demo-app-net"), spec.HostConfig.NetworkMode)
	}
	if !spec.HostConfig.RestartPolicy.IsUnlessStopped() {
		t.Fatalf("expected restart policy unless-stopped, got %#v", spec.HostConfig.RestartPolicy)
	}
	wantMounts := []dockermount.Mount{
		{
			Type:   dockermount.TypeBind,
			Source: filepath.Join("/srv/ovek/projects", "demo-app", pocketBaseDataDirName),
			Target: pocketBaseDataMountPath,
		},
	}
	if !reflect.DeepEqual(spec.HostConfig.Mounts, wantMounts) {
		t.Fatalf("expected mounts %#v, got %#v", wantMounts, spec.HostConfig.Mounts)
	}

	endpoint := spec.NetworkingConfig.EndpointsConfig["demo-app-net"]
	if endpoint == nil {
		t.Fatal("expected network endpoint config for demo-app-net")
	}
	if !reflect.DeepEqual(endpoint.Aliases, []string{pocketBaseNetworkAlias}) {
		t.Fatalf("expected aliases %#v, got %#v", []string{pocketBaseNetworkAlias}, endpoint.Aliases)
	}
}

func TestDockerRuntimeEnsureProjectPocketBaseCreatesAndStartsManagedContainer(t *testing.T) {
	projectsHostDataDir := t.TempDir()
	client := &fakeDockerClient{
		networkInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		networkCreateResponse: dockernetwork.CreateResponse{
			ID: "network-123",
		},
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		containerCreateResponse: dockercontainer.CreateResponse{
			ID: "container-123",
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err != nil {
		t.Fatalf("expected PocketBase provisioning to succeed, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.networkCreateName != "demo-app-net" {
		t.Fatalf("expected project network name %q, got %q", "demo-app-net", client.networkCreateName)
	}
	if client.containerInspectName != "ovek-demo-app-pb" {
		t.Fatalf("expected PocketBase inspect name %q, got %q", "ovek-demo-app-pb", client.containerInspectName)
	}
	if client.containerCreateName != "ovek-demo-app-pb" {
		t.Fatalf("expected PocketBase create name %q, got %q", "ovek-demo-app-pb", client.containerCreateName)
	}
	if client.imagePullRef != defaultPocketBaseImage {
		t.Fatalf("expected PocketBase image pull ref %q, got %q", defaultPocketBaseImage, client.imagePullRef)
	}
	if client.containerCreateConfig == nil || client.containerCreateConfig.Image != defaultPocketBaseImage {
		t.Fatalf("expected create image %q, got %#v", defaultPocketBaseImage, client.containerCreateConfig)
	}
	if client.containerCreateHostConfig == nil || client.containerCreateHostConfig.NetworkMode != dockercontainer.NetworkMode("demo-app-net") {
		t.Fatalf("expected create network mode %q, got %#v", dockercontainer.NetworkMode("demo-app-net"), client.containerCreateHostConfig)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected started container ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestPodmanRuntimeEnsureProjectPocketBaseUsesPodmanPuller(t *testing.T) {
	projectsHostDataDir := t.TempDir()
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
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		containerCreateResponse: dockercontainer.CreateResponse{
			ID: "container-123",
		},
	}
	puller := &fakePodmanImagePuller{}
	runtime := &podmanRuntime{
		dockerRuntime:    newDockerRuntime(client),
		puller:           puller,
		registryInsecure: true,
	}

	containerID, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err != nil {
		t.Fatalf("expected podman PocketBase provisioning to succeed, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected docker image pull not to be used, got %q", client.imagePullRef)
	}
	if puller.imageRef != defaultPocketBaseImage {
		t.Fatalf("expected podman puller image ref %q, got %q", defaultPocketBaseImage, puller.imageRef)
	}
	if !puller.registryInsecure {
		t.Fatal("expected podman puller to receive registryInsecure=true")
	}
}

func TestDockerRuntimeEnsureProjectPocketBaseReusesRunningManagedContainer(t *testing.T) {
	projectsHostDataDir := t.TempDir()
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Image: defaultPocketBaseImage,
				Labels: map[string]string{
					managedLabelKey: managedLabelValue,
					projectLabelKey: "demo-app",
					roleLabelKey:    resourceRolePocketBase,
				},
			},
			Mounts: []dockercontainer.MountPoint{
				{
					Source:      filepath.Join(projectsHostDataDir, "demo-app", pocketBaseDataDirName),
					Destination: pocketBaseDataMountPath,
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net": {
						Aliases: []string{"demo-app-pb", pocketBaseNetworkAlias},
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err != nil {
		t.Fatalf("expected existing PocketBase container to be reused, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected image pull not to be called for an existing container, got %q", client.imagePullRef)
	}
	if client.containerCreateName != "" {
		t.Fatalf("expected create not to be called, got %q", client.containerCreateName)
	}
	if client.containerStartID != "" {
		t.Fatalf("expected running container not to be started again, got %q", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectPocketBaseStartsStoppedManagedContainer(t *testing.T) {
	projectsHostDataDir := t.TempDir()
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: false,
				},
			},
			Config: &dockercontainer.Config{
				Image: defaultPocketBaseImage,
				Labels: map[string]string{
					managedLabelKey: managedLabelValue,
					projectLabelKey: "demo-app",
					roleLabelKey:    resourceRolePocketBase,
				},
			},
			Mounts: []dockercontainer.MountPoint{
				{
					Source:      filepath.Join(projectsHostDataDir, "demo-app", pocketBaseDataDirName),
					Destination: pocketBaseDataMountPath,
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net": {
						Aliases: []string{pocketBaseNetworkAlias},
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	containerID, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err != nil {
		t.Fatalf("expected stopped PocketBase container to be started, got error: %v", err)
	}

	if containerID != "container-123" {
		t.Fatalf("expected container ID %q, got %q", "container-123", containerID)
	}
	if client.imagePullRef != "" {
		t.Fatalf("expected image pull not to be called for an existing container, got %q", client.imagePullRef)
	}
	if client.containerStartID != "container-123" {
		t.Fatalf("expected container start ID %q, got %q", "container-123", client.containerStartID)
	}
}

func TestDockerRuntimeEnsureProjectPocketBasePullsImageBeforeCreate(t *testing.T) {
	projectsHostDataDir := t.TempDir()
	client := &fakeDockerClient{
		networkInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		networkCreateResponse: dockernetwork.CreateResponse{
			ID: "network-123",
		},
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
		imagePullErr:        fmt.Errorf("pull failed"),
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err == nil {
		t.Fatal("expected PocketBase provisioning to fail when image pull fails")
	}
	if !strings.Contains(err.Error(), "pull image") {
		t.Fatalf("expected pull image error, got %v", err)
	}
	if client.containerCreateName != "" {
		t.Fatalf("expected container create not to be called after pull failure, got %q", client.containerCreateName)
	}
}

func TestValidateExistingPocketBaseContainerAcceptsCanonicalizedImageRef(t *testing.T) {
	spec := newPocketBaseContainerSpec(pocketBaseSpec{
		ProjectName:         "demo-app",
		Image:               "elestio/pocketbase:latest",
		ProjectsHostDataDir: "/srv/ovek/projects",
		Network: projectNetwork{
			ID:   "network-123",
			Name: "demo-app-net",
		},
	})

	container := dockercontainer.InspectResponse{
		Config: &dockercontainer.Config{
			Image: "docker.io/elestio/pocketbase:latest",
			Labels: map[string]string{
				managedLabelKey: managedLabelValue,
				projectLabelKey: "demo-app",
				roleLabelKey:    resourceRolePocketBase,
			},
		},
		Mounts: []dockercontainer.MountPoint{
			{
				Source:      spec.HostDataDir,
				Destination: pocketBaseDataMountPath,
			},
		},
		NetworkSettings: &dockercontainer.NetworkSettings{
			Networks: map[string]*dockernetwork.EndpointSettings{
				"demo-app-net": {
					Aliases: []string{pocketBaseNetworkAlias},
				},
			},
		},
	}

	if err := validateExistingPocketBaseContainer(container, spec); err != nil {
		t.Fatalf("expected canonicalized image refs to be accepted, got %v", err)
	}
}

func TestCanonicalContainerImageRef(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantRef string
	}{
		{
			name:    "docker hub explicit tag",
			input:   "elestio/pocketbase:latest",
			wantRef: "docker.io/elestio/pocketbase:latest",
		},
		{
			name:    "docker hub explicit host",
			input:   "docker.io/elestio/pocketbase:latest",
			wantRef: "docker.io/elestio/pocketbase:latest",
		},
		{
			name:    "docker hub library latest default",
			input:   "busybox",
			wantRef: "docker.io/library/busybox:latest",
		},
		{
			name:    "custom registry keeps host",
			input:   "registry:5000/ovek-demo-app:job-123",
			wantRef: "registry:5000/ovek-demo-app:job-123",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := canonicalContainerImageRef(test.input)
			if got != test.wantRef {
				t.Fatalf("expected canonical ref %q, got %q", test.wantRef, got)
			}
		})
	}
}

func TestDockerRuntimeEnsureProjectPocketBaseRejectsUnmanagedContainer(t *testing.T) {
	projectsHostDataDir := t.TempDir()
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
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Image:  defaultPocketBaseImage,
				Labels: map[string]string{},
			},
			Mounts: []dockercontainer.MountPoint{
				{
					Source:      filepath.Join(projectsHostDataDir, "demo-app", pocketBaseDataDirName),
					Destination: pocketBaseDataMountPath,
				},
			},
			NetworkSettings: &dockercontainer.NetworkSettings{
				Networks: map[string]*dockernetwork.EndpointSettings{
					"demo-app-net": {
						Aliases: []string{pocketBaseNetworkAlias},
					},
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	_, err := runtime.EnsureProjectPocketBase(context.Background(), "demo-app", defaultPocketBaseImage, projectsHostDataDir)
	if err == nil {
		t.Fatal("expected unmanaged PocketBase container to be rejected")
	}
	if !strings.Contains(err.Error(), "already exists but is not managed by ovek") {
		t.Fatalf("expected unmanaged container error, got %v", err)
	}
	if client.containerStartID != "" {
		t.Fatalf("expected unmanaged container not to be started, got %q", client.containerStartID)
	}
}

func TestDockerRuntimeRemoveProjectPocketBaseStopsAndRemovesRunningManagedContainer(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectResponse: dockercontainer.InspectResponse{
			ContainerJSONBase: &dockercontainer.ContainerJSONBase{
				ID: "container-123",
				State: &dockercontainer.State{
					Running: true,
				},
			},
			Config: &dockercontainer.Config{
				Labels: map[string]string{
					managedLabelKey: managedLabelValue,
					projectLabelKey: "demo-app",
					roleLabelKey:    resourceRolePocketBase,
				},
			},
		},
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectPocketBase(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected PocketBase removal to succeed, got error: %v", err)
	}
	if client.containerInspectName != "ovek-demo-app-pb" {
		t.Fatalf("expected inspect name %q, got %q", "ovek-demo-app-pb", client.containerInspectName)
	}
	if client.containerStopID != "container-123" {
		t.Fatalf("expected stop ID %q, got %q", "container-123", client.containerStopID)
	}
	if client.containerRemoveID != "container-123" {
		t.Fatalf("expected remove ID %q, got %q", "container-123", client.containerRemoveID)
	}
}

func TestDockerRuntimeRemoveProjectPocketBaseIgnoresMissingContainer(t *testing.T) {
	client := &fakeDockerClient{
		containerInspectErr: fmt.Errorf("missing: %w", cerrdefs.ErrNotFound),
	}
	runtime := newDockerRuntime(client)

	err := runtime.RemoveProjectPocketBase(context.Background(), "demo-app")
	if err != nil {
		t.Fatalf("expected missing PocketBase removal to be ignored, got error: %v", err)
	}
	if client.containerStopID != "" {
		t.Fatalf("expected stop not to be called, got %q", client.containerStopID)
	}
	if client.containerRemoveID != "" {
		t.Fatalf("expected remove not to be called, got %q", client.containerRemoveID)
	}
}
