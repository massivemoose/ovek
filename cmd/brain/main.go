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

	runtime, err := newRuntimeFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create runtime: %v", err)
	}
	ingress := newTraefikFileIngress(db, runtime, cfg.TraefikDynamicConfigDir, cfg.TraefikBrainServiceURL)
	if err := newStartupDeploymentReconciler(db, runtime).Reconcile(context.Background()); err != nil {
		log.Fatalf("failed to reconcile startup deployment state: %v", err)
	}
	if err := reconcileAllProjectStatuses(db); err != nil {
		log.Fatalf("failed to reconcile startup project status state: %v", err)
	}
	if err := ingress.SyncAll(context.Background()); err != nil {
		log.Printf("warning: failed to fully reconcile startup ingress state: %v", err)
	}
	secretCipher, err := loadSecretCipher(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to load secret key: %v", err)
	}
	projectConfigStore := newProjectConfigStore(db, secretCipher)

	processor := newManagedDeploymentProcessor(
		db,
		newBuildProcessor(
			cfg.DataDir,
			cfg.BuildKitHost,
			cfg.BuildRegistryPublishHost,
			cfg.RuntimeRegistryHost,
			cfg.RailpackFrontendImage,
			cfg.RegistryInsecure,
			systemCommandRunner{},
			projectConfigStore,
		),
		runtime,
		cfg.ProjectsHostDataDir,
		cfg.PocketBaseImage,
		projectConfigStore,
	)
	artifactCleaner := newRegistryArtifactCleaner(
		cfg.RuntimeRegistryHost,
		cfg.RegistryAPIBaseURL,
		&http.Client{},
	)
	cleaner := newManagedProjectCleaner(db, runtime, cfg.DataDir, artifactCleaner)
	cleaner.ingress = ingress
	projectRuntimeService := newManagedProjectRuntimeService(db, runtime)
	jobManager := newJobManager(db, processor, artifactCleaner)
	jobManager.ingress = ingress
	workerContext, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	if err := jobManager.Start(workerContext); err != nil {
		log.Fatalf("failed to start job manager: %v", err)
	}

	server := &http.Server{
		Addr:    listenAddr,
		Handler: newHandler(cfg, db, jobManager, cleaner, projectRuntimeService, projectConfigStore),
	}

	log.Printf("brain listening on %s", listenAddr)

	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("brain server failed: %v", err)
	}
}

func newHandler(cfg config, db *sql.DB, enqueuer deploymentEnqueuer, cleaner projectCleanupService, projectRuntimeService projectRuntimeService, configStores ...projectConfigStore) http.Handler {
	var projectConfigStore projectConfigStore
	if len(configStores) > 0 {
		projectConfigStore = configStores[0]
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	apiMux := http.NewServeMux()
	apiMux.HandleFunc("GET /v1/ping", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("pong"))
	})
	apiMux.HandleFunc("POST /v1/auth/reauth", handleReauth(db))
	apiMux.HandleFunc("GET /v1/projects", handleListProjects(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}", handleGetProject(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/deployments", handleListProjectDeployments(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/deployments/{deploymentID}", handleGetProjectDeployment(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/jobs", handleListProjectJobs(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime", handleGetProjectRuntime(projectRuntimeService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime/logs", handleGetProjectRuntimeLogs(projectRuntimeService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime/logs/stream", handleGetProjectRuntimeLogsStream(projectRuntimeService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/env", handleListProjectEnvironment(projectConfigStore))
	apiMux.HandleFunc("PUT /v1/projects/{projectName}/env/{name}", requireCriticalReauth(cfg, db, "project_env.authorized", handleSetProjectEnvironment(projectConfigStore)))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/env/{name}", requireCriticalReauth(cfg, db, "project_env.authorized", handleDeleteProjectEnvironment(projectConfigStore)))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/deployments", requireCriticalReauth(cfg, db, "deploy.authorized", handleCreateDeployment(db, enqueuer)))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}", handleGetJob(db))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}/logs", handleGetJobLogs(db, cfg.DataDir))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}/logs/stream", handleGetJobLogsStream(db, cfg.DataDir))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/runtime", requireCriticalReauth(cfg, db, "runtime_delete.authorized", handleDeleteProjectRuntime(cleaner)))

	mux.HandleFunc("POST /v1/auth/bootstrap", handleBootstrapAuth(cfg, db))
	mux.Handle("/v1/", authMiddleware(cfg, db, apiMux))

	return mux
}
