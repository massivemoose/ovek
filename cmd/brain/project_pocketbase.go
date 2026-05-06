package main

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"time"

	"github.com/massivemoose/ovek/internal/brainapi"
)

const (
	projectPocketBaseSuperuserEmailEnv    = "PB_SUPERUSER_EMAIL"
	projectPocketBaseSuperuserPasswordEnv = "PB_SUPERUSER_PASSWORD"
	projectPocketBasePasswordBytes        = 32
	projectPocketBaseUpsertAttempts       = 6
	projectPocketBaseUpsertRetryDelay     = 500 * time.Millisecond
)

var errProjectPocketBaseAlreadyInitialized = errors.New("project PocketBase credentials already initialized")

var sleepForPocketBaseUpsertRetry = sleepWithContext

type projectPocketBaseRuntime interface {
	EnsureProjectPocketBase(ctx context.Context, projectName string, image string, projectsHostDataDir string) (string, error)
	UpsertProjectPocketBaseSuperuser(ctx context.Context, projectName string, email string, password string) error
	ProjectPocketBaseProxyTarget(ctx context.Context, projectName string) (string, error)
	GetProjectPocketBaseRuntime(ctx context.Context, projectName string) (projectRuntimeContainer, bool, error)
}

type projectPocketBaseStore struct {
	db     *sql.DB
	cipher secretCipher
}

type projectPocketBaseCredential struct {
	ProjectName string
	Email       string
	Password    string
	CreatedAt   string
	UpdatedAt   string
}

type projectPocketBaseCredentialMetadata struct {
	ProjectName string
	Email       string
	CreatedAt   string
	UpdatedAt   string
}

type managedProjectPocketBaseService struct {
	db                  *sql.DB
	runtime             projectPocketBaseRuntime
	projectsHostDataDir string
	pocketBaseImage     string
	credentialStore     projectPocketBaseStore
	configStore         projectConfigStore
	httpClient          *http.Client
}

func newProjectPocketBaseStore(db *sql.DB, cipher secretCipher) projectPocketBaseStore {
	return projectPocketBaseStore{db: db, cipher: cipher}
}

func newManagedProjectPocketBaseService(db *sql.DB, runtime projectPocketBaseRuntime, projectsHostDataDir string, pocketBaseImage string, credentialStore projectPocketBaseStore, configStore projectConfigStore) managedProjectPocketBaseService {
	return managedProjectPocketBaseService{
		db:                  db,
		runtime:             runtime,
		projectsHostDataDir: projectsHostDataDir,
		pocketBaseImage:     pocketBaseImage,
		credentialStore:     credentialStore,
		configStore:         configStore,
		httpClient:          &http.Client{},
	}
}

func handleGetProjectPocketBase(service managedProjectPocketBaseService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		status, err := service.Status(r.Context(), projectName)
		if errors.Is(err, errProjectNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodeProjectNotFound, "project not found")
			return
		}
		if err != nil {
			writeJSONError(w, http.StatusInternalServerError, errorCodePocketBaseFailed, "failed to fetch PocketBase status")
			return
		}

		writeJSON(w, http.StatusOK, status)
	}
}

func handleInitProjectPocketBase(service managedProjectPocketBaseService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()

		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		var request brainapi.InitProjectPocketBaseRequest
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil && !errors.Is(err, io.EOF) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidRequestBody, "invalid request body")
			return
		}

		status, err := service.Init(r.Context(), projectName, request, requestActor(r))
		if errors.Is(err, errProjectPocketBaseAlreadyInitialized) {
			writeJSONError(w, http.StatusConflict, errorCodeInvalidPocketBase, err.Error())
			return
		}
		if isInvalidPocketBaseRequest(err) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidPocketBase, err.Error())
			return
		}
		if err != nil {
			log.Printf("failed to initialize PocketBase for project %q: %v", projectName, err)
			writeJSONError(w, http.StatusInternalServerError, errorCodePocketBaseFailed, "failed to initialize PocketBase")
			return
		}

		writeJSON(w, http.StatusOK, status)
	}
}

func handleProxyProjectPocketBase(service managedProjectPocketBaseService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		projectName := strings.TrimSpace(r.PathValue("projectName"))
		if !isValidProjectName(projectName) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidProjectName, "invalid project name")
			return
		}

		if err := service.Proxy(w, r, projectName); errors.Is(err, errProjectRuntimeNotFound) {
			writeJSONError(w, http.StatusNotFound, errorCodePocketBaseNotFound, "PocketBase runtime not found")
			return
		} else if isInvalidPocketBaseRequest(err) {
			writeJSONError(w, http.StatusBadRequest, errorCodeInvalidPocketBase, err.Error())
			return
		} else if err != nil {
			writeJSONError(w, http.StatusBadGateway, errorCodePocketBaseFailed, "failed to proxy PocketBase request")
			return
		}
	}
}

func (service managedProjectPocketBaseService) Status(ctx context.Context, projectName string) (brainapi.ProjectPocketBaseStatus, error) {
	exists, err := projectExists(service.db, projectName)
	if err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	if !exists {
		return brainapi.ProjectPocketBaseStatus{}, errProjectNotFound
	}

	return service.statusForExistingProject(ctx, projectName)
}

func (service managedProjectPocketBaseService) Init(ctx context.Context, projectName string, request brainapi.InitProjectPocketBaseRequest, actor string) (brainapi.ProjectPocketBaseStatus, error) {
	if service.runtime == nil {
		return brainapi.ProjectPocketBaseStatus{}, errors.New("PocketBase runtime is not configured")
	}
	email := strings.TrimSpace(request.Email)
	if email != "" {
		if err := validatePocketBaseSuperuserEmail(email); err != nil {
			return brainapi.ProjectPocketBaseStatus{}, err
		}
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	if err := ensureProject(service.db, projectName, now); err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	if _, err := service.runtime.EnsureProjectPocketBase(ctx, projectName, service.pocketBaseImage, service.projectsHostDataDir); err != nil {
		return brainapi.ProjectPocketBaseStatus{}, fmt.Errorf("ensure PocketBase: %w", err)
	}

	credential, created, err := service.credentialStore.GetOrCreate(ctx, projectName, email, now)
	if err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	if err := service.upsertSuperuserWithRetry(ctx, projectName, credential); err != nil {
		if created {
			_ = service.credentialStore.Delete(ctx, projectName)
		}
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	var appSecretsRevisionID string
	if request.AppSecrets {
		revisionID, err := service.configureAppSecrets(ctx, projectName, credential, actor)
		if err != nil {
			return brainapi.ProjectPocketBaseStatus{}, err
		}
		appSecretsRevisionID = revisionID
	}
	_ = insertAuditLog(ctx, service.db, auditLogRecord{
		EventType:   "project_pocketbase.init",
		ProjectName: sql.NullString{String: projectName, Valid: true},
		DetailsJSON: mustDetailsJSON(map[string]any{
			"appSecrets": request.AppSecrets,
			"created":    created,
			"actor":      actor,
		}),
		CreatedAt: time.Now().UTC().Format(time.RFC3339Nano),
	})

	status, err := service.statusForExistingProject(ctx, projectName)
	if err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	status.AppSecretsRevisionID = appSecretsRevisionID
	return status, nil
}

func (service managedProjectPocketBaseService) upsertSuperuserWithRetry(ctx context.Context, projectName string, credential projectPocketBaseCredential) error {
	var lastErr error
	for attempt := 1; attempt <= projectPocketBaseUpsertAttempts; attempt++ {
		err := service.runtime.UpsertProjectPocketBaseSuperuser(ctx, projectName, credential.Email, credential.Password)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryablePocketBaseUpsertError(err) || attempt == projectPocketBaseUpsertAttempts {
			return err
		}
		if waitErr := sleepForPocketBaseUpsertRetry(ctx, projectPocketBaseUpsertRetryDelay); waitErr != nil {
			return fmt.Errorf("%w after retryable PocketBase superuser upsert error: %v", waitErr, lastErr)
		}
	}

	return lastErr
}

func isRetryablePocketBaseUpsertError(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "database is locked") || strings.Contains(message, "sqlite_busy")
}

func (service managedProjectPocketBaseService) Proxy(w http.ResponseWriter, r *http.Request, projectName string) error {
	targetRequestURI := strings.TrimSpace(r.URL.Query().Get("target"))
	if targetRequestURI == "" || !strings.HasPrefix(targetRequestURI, "/") || strings.HasPrefix(targetRequestURI, "//") {
		return errors.New("target must be an absolute path")
	}

	targetBase, err := service.runtime.ProjectPocketBaseProxyTarget(r.Context(), projectName)
	if err != nil {
		return err
	}
	targetURL, err := url.Parse(strings.TrimRight(targetBase, "/") + targetRequestURI)
	if err != nil {
		return fmt.Errorf("parse PocketBase proxy target: %w", err)
	}

	proxyRequest, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL.String(), r.Body)
	if err != nil {
		return err
	}
	copyPocketBaseProxyRequestHeaders(proxyRequest.Header, r.Header)

	response, err := service.httpClient.Do(proxyRequest)
	if err != nil {
		return err
	}
	defer response.Body.Close()

	copyHeaders(w.Header(), response.Header)
	w.WriteHeader(response.StatusCode)
	_, _ = io.Copy(w, response.Body)
	return nil
}

func (service managedProjectPocketBaseService) statusForExistingProject(ctx context.Context, projectName string) (brainapi.ProjectPocketBaseStatus, error) {
	status := brainapi.ProjectPocketBaseStatus{
		ProjectName:   projectName,
		ContainerName: pocketBaseContainerName(projectName),
	}

	if service.runtime != nil {
		runtimeView, found, err := service.runtime.GetProjectPocketBaseRuntime(ctx, projectName)
		if err != nil {
			return brainapi.ProjectPocketBaseStatus{}, err
		}
		if found {
			status.ContainerName = runtimeView.ContainerName
			status.Running = runtimeView.Running
		}
	}

	metadata, found, err := service.credentialStore.Metadata(ctx, projectName)
	if err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	if found {
		status.Initialized = true
		status.SuperuserEmail = &metadata.Email
		status.UpdatedAt = metadata.UpdatedAt
	}

	appSecretsConfigured, err := service.appSecretsConfigured(ctx, projectName)
	if err != nil {
		return brainapi.ProjectPocketBaseStatus{}, err
	}
	status.AppSecretsConfigured = appSecretsConfigured

	return status, nil
}

func (service managedProjectPocketBaseService) configureAppSecrets(ctx context.Context, projectName string, credential projectPocketBaseCredential, actor string) (string, error) {
	if err := validateProjectEnvName(projectPocketBaseSuperuserEmailEnv); err != nil {
		return "", err
	}
	if err := validateProjectEnvName(projectPocketBaseSuperuserPasswordEnv); err != nil {
		return "", err
	}
	if err := validateProjectEnvValue(credential.Email); err != nil {
		return "", err
	}
	if err := validateProjectEnvValue(credential.Password); err != nil {
		return "", err
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		return "", fmt.Errorf("begin PocketBase app secrets transaction: %w", err)
	}
	defer tx.Rollback()

	if err := ensureProjectTx(tx, projectName, now); err != nil {
		return "", err
	}

	entries, err := latestProjectConfigEntryMap(ctx, tx, projectName)
	if err != nil {
		return "", err
	}
	encryptedPassword, err := service.configStore.cipher.Encrypt(credential.Password)
	if err != nil {
		return "", err
	}
	entries[projectPocketBaseSuperuserEmailEnv] = projectConfigEntryRecord{
		ProjectName: projectName,
		Name:        projectPocketBaseSuperuserEmailEnv,
		Value:       credential.Email,
		Secret:      false,
		CreatedAt:   now,
	}
	entries[projectPocketBaseSuperuserPasswordEnv] = projectConfigEntryRecord{
		ProjectName: projectName,
		Name:        projectPocketBaseSuperuserPasswordEnv,
		Value:       encryptedPassword,
		Secret:      true,
		CreatedAt:   now,
	}

	revisionID, err := insertProjectConfigRevision(ctx, tx, projectName, actor, "pocketbase.app_secrets", now, entries)
	if err != nil {
		return "", err
	}
	if err := insertAuditLogTx(tx, projectConfigAuditRecord(projectName, actor, projectPocketBaseSuperuserEmailEnv, false, "set", now)); err != nil {
		return "", err
	}
	if err := insertAuditLogTx(tx, projectConfigAuditRecord(projectName, actor, projectPocketBaseSuperuserPasswordEnv, true, "set", now)); err != nil {
		return "", err
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit PocketBase app secrets transaction: %w", err)
	}

	return revisionID, nil
}

func (service managedProjectPocketBaseService) appSecretsConfigured(ctx context.Context, projectName string) (bool, error) {
	revisionID, found, err := latestProjectConfigRevisionID(ctx, service.db, projectName)
	if err != nil {
		return false, err
	}
	if !found {
		return false, nil
	}

	records, err := listProjectConfigEntryRecords(ctx, service.db, projectName, revisionID)
	if err != nil {
		return false, err
	}

	emailConfigured := false
	passwordConfigured := false
	for _, record := range records {
		switch record.Name {
		case projectPocketBaseSuperuserEmailEnv:
			emailConfigured = !record.Secret && strings.TrimSpace(record.Value) != ""
		case projectPocketBaseSuperuserPasswordEnv:
			passwordConfigured = record.Secret && strings.TrimSpace(record.Value) != ""
		}
	}

	return emailConfigured && passwordConfigured, nil
}

func (store projectPocketBaseStore) GetOrCreate(ctx context.Context, projectName string, requestedEmail string, now string) (projectPocketBaseCredential, bool, error) {
	credential, found, err := store.Get(ctx, projectName)
	if err != nil {
		return projectPocketBaseCredential{}, false, err
	}
	if found {
		if requestedEmail != "" && !strings.EqualFold(requestedEmail, credential.Email) {
			return projectPocketBaseCredential{}, false, fmt.Errorf("%w with email %s", errProjectPocketBaseAlreadyInitialized, credential.Email)
		}
		return credential, false, nil
	}

	email := requestedEmail
	if email == "" {
		email = defaultPocketBaseSuperuserEmail(projectName)
	}
	password, err := newProjectPocketBasePassword()
	if err != nil {
		return projectPocketBaseCredential{}, false, err
	}

	credential = projectPocketBaseCredential{
		ProjectName: projectName,
		Email:       email,
		Password:    password,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := store.Insert(ctx, credential); err != nil {
		return projectPocketBaseCredential{}, false, err
	}

	return credential, true, nil
}

func (store projectPocketBaseStore) Get(ctx context.Context, projectName string) (projectPocketBaseCredential, bool, error) {
	var credential projectPocketBaseCredential
	var encryptedPassword string
	err := store.db.QueryRowContext(
		ctx,
		`SELECT project_name, superuser_email, encrypted_password, created_at, updated_at
		 FROM project_pocketbase_credentials
		 WHERE project_name = ?`,
		projectName,
	).Scan(&credential.ProjectName, &credential.Email, &encryptedPassword, &credential.CreatedAt, &credential.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return projectPocketBaseCredential{}, false, nil
	}
	if err != nil {
		return projectPocketBaseCredential{}, false, fmt.Errorf("load PocketBase credentials: %w", err)
	}

	password, err := store.cipher.Decrypt(encryptedPassword)
	if err != nil {
		return projectPocketBaseCredential{}, false, fmt.Errorf("decrypt PocketBase credentials: %w", err)
	}
	credential.Password = password

	return credential, true, nil
}

func (store projectPocketBaseStore) Metadata(ctx context.Context, projectName string) (projectPocketBaseCredentialMetadata, bool, error) {
	var metadata projectPocketBaseCredentialMetadata
	err := store.db.QueryRowContext(
		ctx,
		`SELECT project_name, superuser_email, created_at, updated_at
		 FROM project_pocketbase_credentials
		 WHERE project_name = ?`,
		projectName,
	).Scan(&metadata.ProjectName, &metadata.Email, &metadata.CreatedAt, &metadata.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return projectPocketBaseCredentialMetadata{}, false, nil
	}
	if err != nil {
		return projectPocketBaseCredentialMetadata{}, false, fmt.Errorf("load PocketBase credential metadata: %w", err)
	}

	return metadata, true, nil
}

func (store projectPocketBaseStore) Insert(ctx context.Context, credential projectPocketBaseCredential) error {
	encryptedPassword, err := store.cipher.Encrypt(credential.Password)
	if err != nil {
		return err
	}

	if _, err := store.db.ExecContext(
		ctx,
		`INSERT INTO project_pocketbase_credentials(project_name, superuser_email, encrypted_password, created_at, updated_at)
		 VALUES(?, ?, ?, ?, ?)`,
		credential.ProjectName,
		credential.Email,
		encryptedPassword,
		credential.CreatedAt,
		credential.UpdatedAt,
	); err != nil {
		return fmt.Errorf("insert PocketBase credentials: %w", err)
	}

	return nil
}

func (store projectPocketBaseStore) Delete(ctx context.Context, projectName string) error {
	_, err := store.db.ExecContext(ctx, `DELETE FROM project_pocketbase_credentials WHERE project_name = ?`, projectName)
	return err
}

func ensureProject(db *sql.DB, projectName string, createdAt string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if err := ensureProjectTx(tx, projectName, createdAt); err != nil {
		return err
	}

	return tx.Commit()
}

func validatePocketBaseSuperuserEmail(email string) error {
	parsed, err := mail.ParseAddress(email)
	if err != nil || parsed.Address != email {
		return fmt.Errorf("invalid PocketBase superuser email")
	}

	return nil
}

func defaultPocketBaseSuperuserEmail(projectName string) string {
	return "admin@" + projectName + ".ovek.local"
}

func newProjectPocketBasePassword() (string, error) {
	secret := make([]byte, projectPocketBasePasswordBytes)
	if _, err := rand.Read(secret); err != nil {
		return "", fmt.Errorf("generate PocketBase password: %w", err)
	}

	return base64.RawURLEncoding.EncodeToString(secret), nil
}

func isInvalidPocketBaseRequest(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "PocketBase superuser email") ||
		strings.Contains(message, "target must")
}

func copyPocketBaseProxyRequestHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		if strings.EqualFold(key, headerAPIKey) ||
			strings.EqualFold(key, headerReauthToken) ||
			strings.EqualFold(key, "Host") {
			continue
		}
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}

func copyHeaders(dst http.Header, src http.Header) {
	for key, values := range src {
		for _, value := range values {
			dst.Add(key, value)
		}
	}
}
