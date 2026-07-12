package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

const defaultDataDir = "/var/lib/ovek"
const defaultBuildKitHost = "docker-container://buildkit"
const defaultProjectsHostDataDir = "/var/lib/ovek/projects"
const defaultPocketBaseImage = "ghcr.io/massivemoose/ovek-pocketbase:v0.38.1"
const defaultBuildRegistryPublishHost = "host.docker.internal:5001"
const defaultRuntimeRegistryHost = "localhost:5001"
const defaultRegistryAPIBaseURL = "http://registry:5000"
const defaultRailpackFrontendImage = "ghcr.io/railwayapp/railpack-frontend"
const defaultRegistryInsecure = true
const defaultAuthMode = authModeDev
const defaultRuntimeEngine = runtimeEnginePodman
const defaultTraefikDynamicConfigDir = "/var/lib/ovek/traefik/dynamic"
const defaultTraefikBrainServiceURL = "http://brain:8081"
const defaultAppBrainURL = "http://brain:8081"
const defaultPodmanRuntimeHost = "unix:///run/podman/podman.sock"

type config struct {
	AuthMode                 string
	BrainAPIKey              string
	DataDir                  string
	RuntimeEngine            string
	RuntimeHost              string
	BuildKitHost             string
	BuildRegistryPublishHost string
	RuntimeRegistryHost      string
	RegistryAPIBaseURL       string
	RailpackFrontendImage    string
	RegistryInsecure         bool
	ProjectsHostDataDir      string
	PocketBaseImage          string
	TraefikDynamicConfigDir  string
	TraefikBrainServiceURL   string
	AppBrainURL              string
	PublicBaseDomain         string
	PublicAppsEnabled        bool
	PublicBrainEnabled       bool
	ACMEEmail                string
	WorkflowRunTimeout       time.Duration
}

func loadConfig() (config, error) {
	authMode := strings.TrimSpace(os.Getenv("OVEK_AUTH_MODE"))
	if authMode == "" {
		authMode = defaultAuthMode
	}
	if authMode != authModeDev && authMode != authModeProd {
		return config{}, errors.New("OVEK_AUTH_MODE must be dev or prod")
	}

	apiKey := strings.TrimSpace(os.Getenv("BRAIN_API_KEY"))
	if authMode == authModeDev && apiKey == "" {
		return config{}, errors.New("BRAIN_API_KEY is required in dev auth mode")
	}

	runtimeEngine := strings.TrimSpace(os.Getenv("RUNTIME_ENGINE"))
	if runtimeEngine == "" {
		runtimeEngine = defaultRuntimeEngine
	}
	if runtimeEngine != runtimeEnginePodman {
		return config{}, errors.New("RUNTIME_ENGINE must be podman")
	}

	runtimeHost := strings.TrimSpace(os.Getenv("RUNTIME_HOST"))
	if runtimeHost == "" {
		runtimeHost = defaultPodmanRuntimeHost
	}

	buildKitHost := strings.TrimSpace(os.Getenv("BUILDKIT_HOST"))
	if buildKitHost == "" {
		buildKitHost = defaultBuildKitHost
	}

	buildRegistryPublishHost := strings.TrimSpace(os.Getenv("BUILD_REGISTRY_PUBLISH_HOST"))
	if buildRegistryPublishHost == "" {
		buildRegistryPublishHost = defaultBuildRegistryPublishHost
	}

	runtimeRegistryHost := strings.TrimSpace(os.Getenv("RUNTIME_REGISTRY_HOST"))
	if runtimeRegistryHost == "" {
		runtimeRegistryHost = defaultRuntimeRegistryHost
	}

	registryAPIBaseURL := strings.TrimSpace(os.Getenv("REGISTRY_API_BASE_URL"))
	if registryAPIBaseURL == "" {
		registryAPIBaseURL = defaultRegistryAPIBaseURL
	}

	railpackFrontendImage := strings.TrimSpace(os.Getenv("RAILPACK_FRONTEND_IMAGE"))
	if railpackFrontendImage == "" {
		railpackFrontendImage = defaultRailpackFrontendImage
	}

	registryInsecure := defaultRegistryInsecure
	if registryInsecureValue := strings.TrimSpace(os.Getenv("REGISTRY_INSECURE")); registryInsecureValue != "" {
		parsedRegistryInsecure, err := strconv.ParseBool(registryInsecureValue)
		if err != nil {
			return config{}, errors.New("REGISTRY_INSECURE must be a boolean")
		}
		registryInsecure = parsedRegistryInsecure
	}

	projectsHostDataDir := strings.TrimSpace(os.Getenv("PROJECTS_HOST_DATA_DIR"))
	if projectsHostDataDir == "" {
		projectsHostDataDir = defaultProjectsHostDataDir
	}

	pocketBaseImage := strings.TrimSpace(os.Getenv("POCKETBASE_IMAGE"))
	if pocketBaseImage == "" {
		pocketBaseImage = defaultPocketBaseImage
	}

	traefikDynamicConfigDir := strings.TrimSpace(os.Getenv("TRAEFIK_DYNAMIC_CONFIG_DIR"))
	if traefikDynamicConfigDir == "" {
		traefikDynamicConfigDir = defaultTraefikDynamicConfigDir
	}

	traefikBrainServiceURL := strings.TrimSpace(os.Getenv("TRAEFIK_BRAIN_SERVICE_URL"))
	if traefikBrainServiceURL == "" {
		traefikBrainServiceURL = defaultTraefikBrainServiceURL
	}

	appBrainURL := strings.TrimSpace(os.Getenv("OVEK_APP_BRAIN_URL"))
	if appBrainURL == "" {
		appBrainURL = defaultAppBrainURL
	}

	publicBaseDomain := strings.TrimSpace(os.Getenv("OVEK_PUBLIC_BASE_DOMAIN"))
	publicAppsEnabled := false
	if value := strings.TrimSpace(os.Getenv("OVEK_PUBLIC_APPS_ENABLED")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return config{}, errors.New("OVEK_PUBLIC_APPS_ENABLED must be a boolean")
		}
		publicAppsEnabled = parsed
	}
	publicBrainEnabled := false
	if value := strings.TrimSpace(os.Getenv("OVEK_PUBLIC_BRAIN_ENABLED")); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			return config{}, errors.New("OVEK_PUBLIC_BRAIN_ENABLED must be a boolean")
		}
		publicBrainEnabled = parsed
	}
	acmeEmail := strings.TrimSpace(os.Getenv("OVEK_ACME_EMAIL"))
	if (publicAppsEnabled || publicBrainEnabled) && publicBaseDomain == "" {
		return config{}, errors.New("OVEK_PUBLIC_BASE_DOMAIN is required when public hosting is enabled")
	}
	if publicBrainEnabled && !publicAppsEnabled {
		return config{}, errors.New("OVEK_PUBLIC_BRAIN_ENABLED requires OVEK_PUBLIC_APPS_ENABLED")
	}

	workflowRunTimeout := defaultWorkflowRunTimeout
	if workflowRunTimeoutValue := strings.TrimSpace(os.Getenv("OVEK_WORKFLOW_RUN_TIMEOUT")); workflowRunTimeoutValue != "" {
		parsedWorkflowRunTimeout, err := time.ParseDuration(workflowRunTimeoutValue)
		if err != nil {
			return config{}, errors.New("OVEK_WORKFLOW_RUN_TIMEOUT must be a valid duration")
		}
		if parsedWorkflowRunTimeout <= 0 {
			return config{}, errors.New("OVEK_WORKFLOW_RUN_TIMEOUT must be positive")
		}
		workflowRunTimeout = parsedWorkflowRunTimeout
	}

	return config{
		AuthMode:                 authMode,
		BrainAPIKey:              apiKey,
		DataDir:                  defaultDataDir,
		RuntimeEngine:            runtimeEngine,
		RuntimeHost:              runtimeHost,
		BuildKitHost:             buildKitHost,
		BuildRegistryPublishHost: buildRegistryPublishHost,
		RuntimeRegistryHost:      runtimeRegistryHost,
		RegistryAPIBaseURL:       registryAPIBaseURL,
		RailpackFrontendImage:    railpackFrontendImage,
		RegistryInsecure:         registryInsecure,
		ProjectsHostDataDir:      projectsHostDataDir,
		PocketBaseImage:          pocketBaseImage,
		TraefikDynamicConfigDir:  traefikDynamicConfigDir,
		TraefikBrainServiceURL:   traefikBrainServiceURL,
		AppBrainURL:              appBrainURL,
		PublicBaseDomain:         publicBaseDomain,
		PublicAppsEnabled:        publicAppsEnabled,
		PublicBrainEnabled:       publicBrainEnabled,
		ACMEEmail:                acmeEmail,
		WorkflowRunTimeout:       workflowRunTimeout,
	}, nil
}
