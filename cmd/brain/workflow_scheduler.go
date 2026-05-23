package main

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
)

type workflowScheduleController interface {
	Replace(workflow workflowDefinition) error
	Remove(projectName string, workflowName string)
}

type workflowScheduler struct {
	db       *sql.DB
	enqueuer workflowRunEnqueuer
	cron     *cron.Cron
	mu       sync.Mutex
	entries  map[string]cron.EntryID
}

func newWorkflowScheduler(db *sql.DB, enqueuer workflowRunEnqueuer) *workflowScheduler {
	return &workflowScheduler{
		db:       db,
		enqueuer: enqueuer,
		cron:     cron.New(cron.WithLocation(time.UTC)),
		entries:  make(map[string]cron.EntryID),
	}
}

func (scheduler *workflowScheduler) Start(ctx context.Context) error {
	workflows, err := listScheduledWorkflowDefinitions(ctx, scheduler.db)
	if err != nil {
		return err
	}
	for _, workflow := range workflows {
		if err := scheduler.Replace(workflow); err != nil {
			return err
		}
	}

	scheduler.cron.Start()
	go func() {
		<-ctx.Done()
		scheduler.cron.Stop()
	}()
	return nil
}

func (scheduler *workflowScheduler) Replace(workflow workflowDefinition) error {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	key := workflowScheduleKey(workflow.ProjectName, workflow.Name)
	if entryID, ok := scheduler.entries[key]; ok {
		scheduler.cron.Remove(entryID)
		delete(scheduler.entries, key)
	}
	if !workflow.Enabled || workflow.Schedule == "" {
		return nil
	}

	entryID, err := scheduler.cron.AddFunc(workflow.Schedule, func() {
		run, err := createScheduledWorkflowRun(context.Background(), scheduler.db, workflow.ProjectName, workflow.Name)
		if err != nil {
			log.Printf("failed to create scheduled workflow run for %s/%s: %v", workflow.ProjectName, workflow.Name, err)
			return
		}
		if run.Status == workflowRunStatusQueued && scheduler.enqueuer != nil {
			scheduler.enqueuer.Enqueue(run.ID)
		}
	})
	if err != nil {
		return fmt.Errorf("schedule workflow %s/%s: %w", workflow.ProjectName, workflow.Name, err)
	}
	scheduler.entries[key] = entryID
	return nil
}

func (scheduler *workflowScheduler) Remove(projectName string, workflowName string) {
	scheduler.mu.Lock()
	defer scheduler.mu.Unlock()

	key := workflowScheduleKey(projectName, workflowName)
	if entryID, ok := scheduler.entries[key]; ok {
		scheduler.cron.Remove(entryID)
		delete(scheduler.entries, key)
	}
}

func workflowScheduleKey(projectName string, workflowName string) string {
	return projectName + "/" + workflowName
}

func listScheduledWorkflowDefinitions(ctx context.Context, db *sql.DB) ([]workflowDefinition, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT project_name, name, source_image_ref, resolved_repo_digest, runtime_image_id, schedule, queue_cap, enabled, created_at, updated_at
		 FROM workflows
		 WHERE enabled = 1
		   AND schedule IS NOT NULL
		   AND schedule != ''
		 ORDER BY project_name ASC, name ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list scheduled workflows: %w", err)
	}
	defer rows.Close()

	var workflows []workflowDefinition
	for rows.Next() {
		workflow, err := scanWorkflowDefinition(rows)
		if err != nil {
			return nil, err
		}
		workflows = append(workflows, decorateWorkflowDefinition(workflow))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scheduled workflows: %w", err)
	}

	return workflows, nil
}
