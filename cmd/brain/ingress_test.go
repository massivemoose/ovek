package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
)

func TestTraefikFileIngressEnsureBrainRouteWritesStaticConfig(t *testing.T) {
	db := newTestDB(t)
	configDir := t.TempDir()
	ingress := newTraefikFileIngress(db, fakeAppTargetResolver{}, configDir, "http://brain:8081")

	if err := ingress.EnsureBrainRoute(); err != nil {
		t.Fatalf("expected brain ingress write to succeed, got error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(configDir, brainIngressFileName))
	if err != nil {
		t.Fatalf("expected brain ingress file to exist, got error: %v", err)
	}
	if got := string(body); !strings.Contains(got, "rule: Host(`brain.localhost`)") || !strings.Contains(got, "url: http://brain:8081") {
		t.Fatalf("expected brain ingress config, got %q", got)
	}
}

func TestTraefikFileIngressSyncProjectWritesCurrentDeploymentRoute(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "localhost:5001/alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-19T00:00:00Z",
	})

	configDir := t.TempDir()
	ingress := newTraefikFileIngress(db, fakeAppTargetResolver{
		targets: map[string]appReadinessTarget{
			"demo-app/dep-current": {
				Address: "172.20.0.5:8080",
				URL:     "http://172.20.0.5:8080",
			},
		},
	}, configDir, "")

	if err := ingress.SyncProject(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected project ingress sync to succeed, got error: %v", err)
	}

	body, err := os.ReadFile(filepath.Join(configDir, projectIngressFileName("demo-app")))
	if err != nil {
		t.Fatalf("expected project ingress file to exist, got error: %v", err)
	}
	if got := string(body); !strings.Contains(got, "rule: Host(`demo-app.localhost`)") || !strings.Contains(got, "url: http://172.20.0.5:8080") {
		t.Fatalf("expected project ingress config, got %q", got)
	}
}

func TestTraefikFileIngressSyncProjectRemovesRouteWhenProjectHasNoCurrentDeployment(t *testing.T) {
	db := newTestDB(t)
	seedProjectRecord(t, db, "demo-app")
	configDir := t.TempDir()
	path := filepath.Join(configDir, projectIngressFileName("demo-app"))
	if err := os.WriteFile(path, []byte("stale"), 0o644); err != nil {
		t.Fatalf("expected stale config write to succeed, got error: %v", err)
	}

	ingress := newTraefikFileIngress(db, fakeAppTargetResolver{}, configDir, "")
	if err := ingress.SyncProject(context.Background(), "demo-app"); err != nil {
		t.Fatalf("expected project ingress removal to succeed, got error: %v", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected stale config to be removed, got error %v", err)
	}
}

func TestTraefikFileIngressSyncAllRemovesStaleProjectConfigs(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "alpha-app",
		ImageRef:                "localhost:5001/alces-alpha-app:dep-current",
		AppContainerName:        "alces-alpha-app-app-dep-current",
		NetworkName:             "alpha-app-net",
		PocketBaseContainerName: "alces-alpha-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-19T00:00:00Z",
	})
	configDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(configDir, projectIngressFileName("stale-app")), []byte("stale"), 0o644); err != nil {
		t.Fatalf("expected stale config write to succeed, got error: %v", err)
	}

	ingress := newTraefikFileIngress(db, fakeAppTargetResolver{
		targets: map[string]appReadinessTarget{
			"alpha-app/dep-current": {
				Address: "172.20.0.7:8080",
				URL:     "http://172.20.0.7:8080",
			},
		},
	}, configDir, "")

	if err := ingress.SyncAll(context.Background()); err != nil {
		t.Fatalf("expected ingress sync-all to succeed, got error: %v", err)
	}

	entries, err := os.ReadDir(configDir)
	if err != nil {
		t.Fatalf("expected config dir read to succeed, got error: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	if !reflect.DeepEqual(names, []string{brainIngressFileName, projectIngressFileName("alpha-app")}) {
		t.Fatalf("expected only active ingress files, got %#v", names)
	}
}

type fakeAppTargetResolver struct {
	targets map[string]appReadinessTarget
	err     error
}

func (resolver fakeAppTargetResolver) ResolveProjectAppReadinessTarget(_ context.Context, projectName string, deploymentID string) (appReadinessTarget, error) {
	if resolver.err != nil {
		return appReadinessTarget{}, resolver.err
	}

	return resolver.targets[projectName+"/"+deploymentID], nil
}

type recordingProjectIngressManager struct {
	brainEnsured    bool
	syncedProjects  []string
	removedProjects []string
	syncErr         error
	removeErr       error
}

func (manager *recordingProjectIngressManager) EnsureBrainRoute() error {
	manager.brainEnsured = true
	return nil
}

func (manager *recordingProjectIngressManager) SyncProject(_ context.Context, projectName string) error {
	manager.syncedProjects = append(manager.syncedProjects, projectName)
	sort.Strings(manager.syncedProjects)
	return manager.syncErr
}

func (manager *recordingProjectIngressManager) SyncAll(context.Context) error {
	manager.brainEnsured = true
	return manager.syncErr
}

func (manager *recordingProjectIngressManager) RemoveProject(projectName string) error {
	manager.removedProjects = append(manager.removedProjects, projectName)
	sort.Strings(manager.removedProjects)
	return manager.removeErr
}
