package main

import (
	"context"
	"database/sql"
	"testing"
)

func TestStartupDeploymentReconcilerPromotesSingleRunningAppWhenCurrentRuntimeMissing(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-old",
		AppContainerName:        "alces-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})

	runtime := &fakeDeploymentReconcilerRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-new",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-new",
					ImageRef:                "alces-demo-app:dep-new",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
					CreatedAt:               "2026-04-09T00:10:00Z",
					Running:                 true,
				},
			},
		},
	}

	err := newStartupDeploymentReconciler(db, runtime).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("expected startup reconciliation to succeed, got error: %v", err)
	}

	if got := getProjectCurrentDeploymentID(t, db, "demo-app"); got != "dep-new" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-new", got)
	}
	oldDeployment := getDeploymentRecord(t, db, "dep-old")
	if oldDeployment.Status != deploymentStatusSuperseded {
		t.Fatalf("expected old deployment status %q, got %q", deploymentStatusSuperseded, oldDeployment.Status)
	}
	newDeployment := getDeploymentRecord(t, db, "dep-new")
	if newDeployment.Status != deploymentStatusSucceeded {
		t.Fatalf("expected new deployment status %q, got %q", deploymentStatusSucceeded, newDeployment.Status)
	}
	if newDeployment.AppContainerName != "alces-demo-app-app-dep-new" {
		t.Fatalf("expected new app container name %q, got %q", "alces-demo-app-app-dep-new", newDeployment.AppContainerName)
	}
	if len(runtime.removedDeployments) != 0 {
		t.Fatalf("expected no app removals, got %#v", runtime.removedDeployments)
	}
}

func TestStartupDeploymentReconcilerKeepsCurrentRunningAppAndRemovesExtras(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-old",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-old",
		AppContainerName:        "alces-demo-app-app-dep-old",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	seedDeploymentRecord(t, db, deploymentRecord{
		ID:                      "dep-extra",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-extra",
		AppContainerName:        "alces-demo-app-app-dep-extra",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:05:00Z",
	})

	runtime := &fakeDeploymentReconcilerRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-old",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-old",
					ImageRef:                "alces-demo-app:dep-old",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
					CreatedAt:               "2026-04-09T00:00:00Z",
					Running:                 true,
				},
				{
					DeploymentID:            "dep-extra",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-extra",
					ImageRef:                "alces-demo-app:dep-extra",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
					CreatedAt:               "2026-04-09T00:05:00Z",
					Running:                 true,
				},
			},
		},
	}

	err := newStartupDeploymentReconciler(db, runtime).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("expected startup reconciliation to succeed, got error: %v", err)
	}

	if got := getProjectCurrentDeploymentID(t, db, "demo-app"); got != "dep-old" {
		t.Fatalf("expected current deployment ID %q, got %q", "dep-old", got)
	}
	extraDeployment := getDeploymentRecord(t, db, "dep-extra")
	if extraDeployment.Status != deploymentStatusSuperseded {
		t.Fatalf("expected extra deployment status %q, got %q", deploymentStatusSuperseded, extraDeployment.Status)
	}
	if got := len(runtime.removedDeployments); got != 1 {
		t.Fatalf("expected 1 app removal, got %d", got)
	}
	if runtime.removedDeployments[0].ID != "dep-extra" {
		t.Fatalf("expected removed deployment ID %q, got %q", "dep-extra", runtime.removedDeployments[0].ID)
	}
}

func TestStartupDeploymentReconcilerSkipsAmbiguousMultipleRunningAppsWithoutCurrent(t *testing.T) {
	db := newTestDB(t)
	seedProjectRecord(t, db, "demo-app")

	runtime := &fakeDeploymentReconcilerRuntime{
		appsByProject: map[string][]projectAppRuntime{
			"demo-app": {
				{
					DeploymentID:            "dep-a",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-a",
					ImageRef:                "alces-demo-app:dep-a",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
					CreatedAt:               "2026-04-09T00:00:00Z",
					Running:                 true,
				},
				{
					DeploymentID:            "dep-b",
					ProjectName:             "demo-app",
					AppContainerName:        "alces-demo-app-app-dep-b",
					ImageRef:                "alces-demo-app:dep-b",
					NetworkName:             "demo-app-net",
					PocketBaseContainerName: "alces-demo-app-pb",
					CreatedAt:               "2026-04-09T00:01:00Z",
					Running:                 true,
				},
			},
		},
	}

	err := newStartupDeploymentReconciler(db, runtime).Reconcile(context.Background())
	if err != nil {
		t.Fatalf("expected startup reconciliation to succeed, got error: %v", err)
	}

	assertCurrentDeploymentUnset(t, db, "demo-app")
	assertDeploymentMissing(t, db, "dep-a")
	assertDeploymentMissing(t, db, "dep-b")
	if len(runtime.removedDeployments) != 0 {
		t.Fatalf("expected no app removals, got %#v", runtime.removedDeployments)
	}
}

type fakeDeploymentReconcilerRuntime struct {
	appsByProject      map[string][]projectAppRuntime
	listErr            error
	removeErr          error
	removedDeployments []deploymentRecord
}

func (runtime *fakeDeploymentReconcilerRuntime) ListProjectApps(_ context.Context, projectName string) ([]projectAppRuntime, error) {
	if runtime.listErr != nil {
		return nil, runtime.listErr
	}

	return append([]projectAppRuntime(nil), runtime.appsByProject[projectName]...), nil
}

func (runtime *fakeDeploymentReconcilerRuntime) RemoveProjectApp(_ context.Context, deployment deploymentRecord) error {
	runtime.removedDeployments = append(runtime.removedDeployments, deployment)

	return runtime.removeErr
}

func seedProjectRecord(t *testing.T, db *sql.DB, projectName string) {
	t.Helper()

	if _, err := db.Exec(
		"INSERT OR IGNORE INTO projects(name, status, created_at) VALUES(?, ?, ?)",
		projectName,
		projectStatusIdle,
		"2026-04-09T00:00:00Z",
	); err != nil {
		t.Fatalf("expected project seed to succeed, got error: %v", err)
	}
}

func seedDeploymentRecord(t *testing.T, db *sql.DB, deployment deploymentRecord) {
	t.Helper()

	seedProjectRecord(t, db, deployment.ProjectName)
	if _, err := db.Exec(
		`INSERT INTO deployments(id, project_name, image_ref, app_container_name, network_name, pb_container_name, status, created_at)
		 VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		deployment.ID,
		deployment.ProjectName,
		deployment.ImageRef,
		deployment.AppContainerName,
		deployment.NetworkName,
		deployment.PocketBaseContainerName,
		deployment.Status,
		deployment.CreatedAt,
	); err != nil {
		t.Fatalf("expected deployment seed to succeed, got error: %v", err)
	}
}
