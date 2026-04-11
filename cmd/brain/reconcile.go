package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
)

type deploymentReconcilerRuntime interface {
	ListProjectApps(ctx context.Context, projectName string) ([]projectAppRuntime, error)
	RemoveProjectApp(ctx context.Context, deployment deploymentRecord) error
}

type startupDeploymentReconciler struct {
	db      *sql.DB
	runtime deploymentReconcilerRuntime
}

func newStartupDeploymentReconciler(db *sql.DB, runtime deploymentReconcilerRuntime) startupDeploymentReconciler {
	return startupDeploymentReconciler{
		db:      db,
		runtime: runtime,
	}
}

func (reconciler startupDeploymentReconciler) Reconcile(ctx context.Context) error {
	projectNames, err := listProjectNames(reconciler.db)
	if err != nil {
		return err
	}

	for _, projectName := range projectNames {
		if err := reconciler.reconcileProject(ctx, projectName); err != nil {
			log.Printf("failed to reconcile project %q deployment state: %v", projectName, err)
		}
	}

	return nil
}

func (reconciler startupDeploymentReconciler) reconcileProject(ctx context.Context, projectName string) error {
	currentDeployment, hasCurrentDeployment, err := getProjectCurrentDeployment(reconciler.db, projectName)
	if err != nil {
		return err
	}

	apps, err := reconciler.runtime.ListProjectApps(ctx, projectName)
	if err != nil {
		return err
	}

	runningApps := filterRunningProjectApps(apps)
	if hasCurrentDeployment {
		if _, found := findProjectAppByDeploymentID(runningApps, currentDeployment.ID); found {
			if err := normalizeProjectCurrentDeployment(reconciler.db, currentDeployment); err != nil {
				return err
			}
			if err := reconciler.removeOtherProjectApps(ctx, apps, currentDeployment.ID); err != nil {
				return err
			}

			return nil
		}
	}

	if len(runningApps) == 1 {
		runningApp := runningApps[0]
		if err := promoteProjectAppRuntime(reconciler.db, runningApp); err != nil {
			return err
		}
		if err := reconciler.removeOtherProjectApps(ctx, apps, runningApp.DeploymentID); err != nil {
			return err
		}

		if hasCurrentDeployment {
			log.Printf(
				"reconciled project %q by promoting running app deployment %q over missing current deployment %q",
				projectName,
				runningApp.DeploymentID,
				currentDeployment.ID,
			)
		} else {
			log.Printf(
				"reconciled project %q by adopting running app deployment %q as current",
				projectName,
				runningApp.DeploymentID,
			)
		}

		return nil
	}

	switch {
	case hasCurrentDeployment && len(runningApps) == 0 && len(apps) == 0:
		log.Printf(
			"project %q current deployment %q has no managed app container; startup reconciliation skipped",
			projectName,
			currentDeployment.ID,
		)
	case hasCurrentDeployment && len(runningApps) == 0:
		log.Printf(
			"project %q current deployment %q has only stopped managed app containers; startup reconciliation skipped",
			projectName,
			currentDeployment.ID,
		)
	case hasCurrentDeployment:
		log.Printf(
			"project %q has %d running managed app containers and current deployment %q is not among them; startup reconciliation skipped",
			projectName,
			len(runningApps),
			currentDeployment.ID,
		)
	case len(runningApps) == 0 && len(apps) == 0:
		return nil
	case len(runningApps) == 0:
		log.Printf(
			"project %q has only stopped managed app containers and no current deployment; startup reconciliation skipped",
			projectName,
		)
	default:
		log.Printf(
			"project %q has %d running managed app containers and no current deployment; startup reconciliation skipped",
			projectName,
			len(runningApps),
		)
	}

	return nil
}

func (reconciler startupDeploymentReconciler) removeOtherProjectApps(ctx context.Context, apps []projectAppRuntime, keepDeploymentID string) error {
	for _, app := range apps {
		if app.DeploymentID == keepDeploymentID {
			continue
		}

		if err := reconciler.runtime.RemoveProjectApp(ctx, app.deploymentRecord()); err != nil {
			return fmt.Errorf("remove superseded app container %q: %w", app.AppContainerName, err)
		}
	}

	return nil
}

func listProjectNames(db *sql.DB) ([]string, error) {
	rows, err := db.Query(
		`SELECT name
		 FROM projects
		 ORDER BY name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer rows.Close()

	var projectNames []string
	for rows.Next() {
		var projectName string
		if err := rows.Scan(&projectName); err != nil {
			return nil, fmt.Errorf("scan project name: %w", err)
		}
		projectNames = append(projectNames, projectName)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}

	return projectNames, nil
}

func normalizeProjectCurrentDeployment(db *sql.DB, deployment deploymentRecord) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin normalize current deployment transaction: %w", err)
	}

	if err := setDeploymentStatus(tx, deployment.ProjectName, deployment.ID, deploymentStatusSucceeded); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := supersedeOtherProjectDeployments(tx, deployment.ProjectName, deployment.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := setProjectCurrentDeployment(tx, deployment.ProjectName, deployment.ID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit normalize current deployment transaction: %w", err)
	}

	return nil
}

func promoteProjectAppRuntime(db *sql.DB, app projectAppRuntime) error {
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin promote app runtime transaction: %w", err)
	}

	upsertResult, err := tx.Exec(
		`INSERT INTO deployments(
			id,
			project_name,
			image_ref,
			app_container_name,
			network_name,
			pb_container_name,
			status,
			created_at
		) VALUES(?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			image_ref = excluded.image_ref,
			app_container_name = excluded.app_container_name,
			network_name = excluded.network_name,
			pb_container_name = excluded.pb_container_name`,
		app.DeploymentID,
		app.ProjectName,
		app.ImageRef,
		app.AppContainerName,
		app.NetworkName,
		app.PocketBaseContainerName,
		deploymentStatusSucceeded,
		app.CreatedAt,
	)
	if err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("upsert deployment %q from runtime: %w", app.DeploymentID, err)
	}
	if err := requireUpdatedRow(upsertResult, "upsert deployment"); err != nil {
		_ = tx.Rollback()
		return err
	}

	if err := setDeploymentStatus(tx, app.ProjectName, app.DeploymentID, deploymentStatusSucceeded); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := supersedeOtherProjectDeployments(tx, app.ProjectName, app.DeploymentID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := setProjectCurrentDeployment(tx, app.ProjectName, app.DeploymentID); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit promote app runtime transaction: %w", err)
	}

	return nil
}

func setDeploymentStatus(tx *sql.Tx, projectName string, deploymentID string, status string) error {
	updateResult, err := tx.Exec(
		`UPDATE deployments
		 SET status = ?
		 WHERE id = ? AND project_name = ?`,
		status,
		deploymentID,
		projectName,
	)
	if err != nil {
		return fmt.Errorf("set deployment %q status to %q: %w", deploymentID, status, err)
	}

	return requireUpdatedRow(updateResult, "set deployment status")
}

func supersedeOtherProjectDeployments(tx *sql.Tx, projectName string, keepDeploymentID string) error {
	if _, err := tx.Exec(
		`UPDATE deployments
		 SET status = ?
		 WHERE project_name = ? AND id <> ? AND status = ?`,
		deploymentStatusSuperseded,
		projectName,
		keepDeploymentID,
		deploymentStatusSucceeded,
	); err != nil {
		return fmt.Errorf("supersede other deployments for project %q: %w", projectName, err)
	}

	return nil
}

func filterRunningProjectApps(apps []projectAppRuntime) []projectAppRuntime {
	runningApps := make([]projectAppRuntime, 0, len(apps))
	for _, app := range apps {
		if app.Running {
			runningApps = append(runningApps, app)
		}
	}

	return runningApps
}

func findProjectAppByDeploymentID(apps []projectAppRuntime, deploymentID string) (projectAppRuntime, bool) {
	for _, app := range apps {
		if app.DeploymentID == deploymentID {
			return app, true
		}
	}

	return projectAppRuntime{}, false
}

func (app projectAppRuntime) deploymentRecord() deploymentRecord {
	return deploymentRecord{
		ID:                      app.DeploymentID,
		ProjectName:             app.ProjectName,
		ImageRef:                app.ImageRef,
		AppContainerName:        app.AppContainerName,
		NetworkName:             app.NetworkName,
		PocketBaseContainerName: app.PocketBaseContainerName,
		CreatedAt:               app.CreatedAt,
	}
}
