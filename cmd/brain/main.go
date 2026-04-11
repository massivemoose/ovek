package main

import (
	"context"
	"database/sql"
	"errors"
	"log"
	"net/http"
)

const listenAddr = ":8081"

func main() {
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("failed to load config: %v", err)
	}

	db, err := openBrainDB(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to open brain database: %v", err)
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Printf("failed to close brain database: %v", err)
		}
	}()

	runtime, err := newDockerRuntimeFromEnv()
	if err != nil {
		log.Fatalf("failed to create docker runtime: %v", err)
	}
	if err := newStartupDeploymentReconciler(db, runtime).Reconcile(context.Background()); err != nil {
		log.Fatalf("failed to reconcile startup deployment state: %v", err)
	}

	processor := newManagedDeploymentProcessor(
		db,
		newBuildProcessor(cfg.DataDir, cfg.BuildKitHost, systemCommandRunner{}),
		runtime,
		cfg.ProjectsHostDataDir,
		cfg.PocketBaseImage,
	)
	cleaner := newManagedProjectCleaner(db, runtime)
	jobManager := newJobManager(db, processor)
	workerContext, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	if err := jobManager.Start(workerContext); err != nil {
		log.Fatalf("failed to start job manager: %v", err)
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: newHandler(cfg, db, jobManager, cleaner),
	}

	log.Printf("brain listening on %s", listenAddr)

	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("brain server failed: %v", err)
	}
}

func newHandler(cfg config, db *sql.DB, enqueuer deploymentEnqueuer, cleaner projectCleanupService) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	apiMux.HandleFunc("POST /v1/projects/{projectName}/deployments", handleCreateDeployment(db, enqueuer))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}", handleGetJob(db))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/runtime", handleDeleteProjectRuntime(cleaner))

	mux.Handle("/v1/", apiKeyMiddleware(cfg.BrainAPIKey, apiMux))

	return mux
}
