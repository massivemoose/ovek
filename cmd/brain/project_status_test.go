package main

import "testing"

func TestReconcileAllProjectStatusesRepairsLegacyRunningProjectRows(t *testing.T) {
	db := newTestDB(t)
	seedCurrentDeployment(t, db, deploymentRecord{
		ID:                      "dep-current",
		ProjectName:             "demo-app",
		ImageRef:                "alces-demo-app:dep-current",
		AppContainerName:        "alces-demo-app-app-dep-current",
		NetworkName:             "demo-app-net",
		PocketBaseContainerName: "alces-demo-app-pb",
		Status:                  deploymentStatusSucceeded,
		CreatedAt:               "2026-04-09T00:00:00Z",
	})
	if _, err := db.Exec("UPDATE projects SET status = ? WHERE name = ?", projectStatusIdle, "demo-app"); err != nil {
		t.Fatalf("expected legacy status setup to succeed, got error: %v", err)
	}

	if err := reconcileAllProjectStatuses(db); err != nil {
		t.Fatalf("expected status reconciliation to succeed, got error: %v", err)
	}

	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusRunning {
		t.Fatalf("expected project status %q, got %q", projectStatusRunning, got)
	}
}

func TestReconcileAllProjectStatusesRepairsLegacyFailedProjectRows(t *testing.T) {
	db := newTestDB(t)
	seedProjectRecord(t, db, "demo-app")
	if _, err := db.Exec(
		`INSERT INTO jobs(id, project_name, repo_url, status, created_at, finished_at, error_message)
		 VALUES(?, ?, ?, ?, ?, ?, ?)`,
		"job-failed",
		"demo-app",
		"https://example.com/demo.git",
		jobStatusFailed,
		"2026-04-10T00:00:00Z",
		"2026-04-10T00:01:00Z",
		"build failed",
	); err != nil {
		t.Fatalf("expected failed job seed to succeed, got error: %v", err)
	}
	if _, err := db.Exec("UPDATE projects SET status = ? WHERE name = ?", projectStatusIdle, "demo-app"); err != nil {
		t.Fatalf("expected legacy status setup to succeed, got error: %v", err)
	}

	if err := reconcileAllProjectStatuses(db); err != nil {
		t.Fatalf("expected status reconciliation to succeed, got error: %v", err)
	}

	if got := getProjectStatus(t, db, "demo-app"); got != projectStatusFailed {
		t.Fatalf("expected project status %q, got %q", projectStatusFailed, got)
	}
}
