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

	secretCipher, err := loadSecretCipher(cfg.DataDir)
	if err != nil {
		log.Fatalf("failed to load secret key: %v", err)
	}
	projectConfigStore := newProjectConfigStore(db, secretCipher)
	projectPocketBaseStore := newProjectPocketBaseStore(db, secretCipher)
	registryCredentialStore := newRegistryCredentialStore(db, secretCipher)

	runtime, err := newRuntimeFromConfig(cfg, registryCredentialStore)
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
	processor := newManagedDeploymentProcessor(
		db,
		sourceDispatchProcessor{
			repo: newBuildProcessor(
				cfg.DataDir,
				cfg.BuildKitHost,
				cfg.BuildRegistryPublishHost,
				cfg.RuntimeRegistryHost,
				cfg.RailpackFrontendImage,
				cfg.RegistryInsecure,
				systemCommandRunner{},
				projectConfigStore,
			),
			image: newImageProcessor(cfg.DataDir, projectConfigStore),
		},
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
	cleaner.projectsHostDataDir = cfg.ProjectsHostDataDir
	projectRuntimeService := newManagedProjectRuntimeService(db, runtime, ingress)
	projectPocketBaseService := newManagedProjectPocketBaseService(db, runtime, cfg.ProjectsHostDataDir, cfg.PocketBaseImage, projectPocketBaseStore, projectConfigStore)
	jobManager := newJobManager(db, processor, artifactCleaner)
	jobManager.ingress = ingress
	workerContext, cancelWorker := context.WithCancel(context.Background())
	defer cancelWorker()

	if err := jobManager.Start(workerContext); err != nil {
		log.Fatalf("failed to start job manager: %v", err)
	}
	workflowProcessor := newManagedWorkflowProcessor(db, runtime, cfg.DataDir, cfg.ProjectsHostDataDir, cfg.PocketBaseImage, projectConfigStore, cfg.WorkflowRunTimeout)
	workflowManager := newWorkflowManager(db, workflowProcessor)
	if err := workflowManager.Start(workerContext); err != nil {
		log.Fatalf("failed to start workflow manager: %v", err)
	}
	workflowScheduler := newWorkflowScheduler(db, workflowManager)
	if err := workflowScheduler.Start(workerContext); err != nil {
		log.Fatalf("failed to start workflow scheduler: %v", err)
	}

	server := &http.Server{
		Addr: listenAddr,
		Handler: newHandlerWithRegistryStore(
			cfg,
			db,
			jobManager,
			cleaner,
			projectRuntimeService,
			projectPocketBaseService,
			runtime,
			workflowManager,
			workflowScheduler,
			registryCredentialStore,
			projectConfigStore,
		),
	}

	log.Printf("brain listening on %s", listenAddr)

	err = server.ListenAndServe()
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("brain server failed: %v", err)
	}
}

func newHandler(cfg config, db *sql.DB, enqueuer deploymentEnqueuer, cleaner projectCleanupService, projectRuntimeService projectRuntimeService, projectPocketBaseService managedProjectPocketBaseService, configStores ...projectConfigStore) http.Handler {
	return newHandlerWithRegistryStore(cfg, db, enqueuer, cleaner, projectRuntimeService, projectPocketBaseService, passthroughWorkflowImageResolver{}, nil, nil, registryCredentialStore{}, configStores...)
}

func newHandlerWithRegistryStore(cfg config, db *sql.DB, enqueuer deploymentEnqueuer, cleaner projectCleanupService, projectRuntimeService projectRuntimeService, projectPocketBaseService managedProjectPocketBaseService, workflowImages workflowImageResolver, workflowRuns workflowRunEnqueuer, workflowSchedules workflowScheduleController, registryStore registryCredentialStore, configStores ...projectConfigStore) http.Handler {
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
	apiMux.HandleFunc("GET /v1/auth/api-keys", handleListAPIKeys(db))
	apiMux.HandleFunc("POST /v1/auth/api-keys", requireCriticalReauth(cfg, db, "auth.api_key_create.authorized", handleCreateAPIKey(db)))
	apiMux.HandleFunc("DELETE /v1/auth/api-keys/{keyID}", requireCriticalReauth(cfg, db, "auth.api_key_revoke.authorized", handleRevokeAPIKey(db)))
	apiMux.HandleFunc("POST /v1/auth/password", requireCriticalReauth(cfg, db, "auth.password_change.authorized", handleChangePassword(db)))
	apiMux.HandleFunc("GET /v1/projects", handleListProjects(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}", handleGetProject(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/deployments", handleListProjectDeployments(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/deployments/{deploymentID}", handleGetProjectDeployment(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/jobs", handleListProjectJobs(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflows", handleListWorkflowDefinitions(db))
	apiMux.HandleFunc("PUT /v1/projects/{projectName}/workflows/{workflowName}", requireCriticalReauth(cfg, db, "workflow.set.authorized", handleUpsertWorkflowDefinition(db, workflowImages, workflowSchedules)))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflows/{workflowName}", handleGetWorkflowDefinition(db))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/workflows/{workflowName}", requireCriticalReauth(cfg, db, "workflow.delete.authorized", handleDeleteWorkflowDefinition(db, workflowSchedules)))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/workflows/{workflowName}/runs", handleCreateWorkflowRun(db, workflowRuns))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflow-runs", handleListWorkflowRuns(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflow-runs/{runID}", handleGetWorkflowRun(db))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflow-runs/{runID}/logs", handleGetWorkflowRunLogs(db, cfg.DataDir))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/workflow-runs/{runID}/logs/stream", handleGetWorkflowRunLogsStream(db, cfg.DataDir))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime", handleGetProjectRuntime(projectRuntimeService))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/runtime/start", requireCriticalReauth(cfg, db, "runtime_start.authorized", handleStartProjectRuntime(projectRuntimeService)))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/runtime/stop", requireCriticalReauth(cfg, db, "runtime_stop.authorized", handleStopProjectRuntime(projectRuntimeService)))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/runtime/restart", requireCriticalReauth(cfg, db, "runtime_restart.authorized", handleRestartProjectRuntime(projectRuntimeService)))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime/logs", handleGetProjectRuntimeLogs(projectRuntimeService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/runtime/logs/stream", handleGetProjectRuntimeLogsStream(projectRuntimeService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/pocketbase", handleGetProjectPocketBase(projectPocketBaseService))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/pocketbase/init", requireCriticalReauth(cfg, db, "project_pocketbase.authorized", handleInitProjectPocketBase(projectPocketBaseService)))
	apiMux.HandleFunc("/v1/projects/{projectName}/pocketbase/proxy", handleProxyProjectPocketBase(projectPocketBaseService))
	apiMux.HandleFunc("GET /v1/projects/{projectName}/env", handleListProjectEnvironment(projectConfigStore))
	apiMux.HandleFunc("PUT /v1/projects/{projectName}/env/{name}", requireCriticalReauth(cfg, db, "project_env.authorized", handleSetProjectEnvironment(projectConfigStore)))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/env/{name}", requireCriticalReauth(cfg, db, "project_env.authorized", handleDeleteProjectEnvironment(projectConfigStore)))
	if registryStore.db != nil {
		apiMux.HandleFunc("GET /v1/registry/credentials", handleListRegistryCredentials(registryStore))
		apiMux.HandleFunc("PUT /v1/registry/credentials/{host}", requireCriticalReauth(cfg, db, "registry_credential.authorized", handleUpsertRegistryCredential(registryStore)))
		apiMux.HandleFunc("DELETE /v1/registry/credentials/{host}", requireCriticalReauth(cfg, db, "registry_credential.authorized", handleDeleteRegistryCredential(registryStore)))
	}
	apiMux.HandleFunc("POST /v1/projects/{projectName}/deployments", requireCriticalReauth(cfg, db, "deploy.authorized", handleCreateDeployment(db, enqueuer)))
	apiMux.HandleFunc("POST /v1/projects/{projectName}/runs", requireCriticalReauth(cfg, db, "run.authorized", handleCreateRun(db, enqueuer)))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}", handleGetJob(db))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}/logs", handleGetJobLogs(db, cfg.DataDir))
	apiMux.HandleFunc("GET /v1/jobs/{jobID}/logs/stream", handleGetJobLogsStream(db, cfg.DataDir))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/runtime", requireCriticalReauth(cfg, db, "runtime_delete.authorized", handleDeleteProjectRuntime(cleaner)))
	apiMux.HandleFunc("DELETE /v1/projects/{projectName}/runtime/app", requireCriticalReauth(cfg, db, "runtime_remove.authorized", handleDeleteProjectAppRuntime(cleaner)))

	mux.HandleFunc("POST /v1/auth/bootstrap", handleBootstrapAuth(cfg, db))
	mux.Handle("/v1/", authMiddleware(cfg, db, apiMux))

	return mux
}
