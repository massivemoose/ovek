package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

const defaultDataDir = "/var/lib/ovek"
const defaultBuildKitHost = "docker-container://buildkit"
const defaultProjectsHostDataDir = "/var/lib/ovek/projects"
const defaultPocketBaseImage = "docker.io/elestio/pocketbase:latest"
const defaultBuildRegistryPublishHost = "host.docker.internal:5001"
const defaultRuntimeRegistryHost = "localhost:5001"
const defaultRegistryAPIBaseURL = "http://registry:5000"
const defaultRailpackFrontendImage = "ghcr.io/railwayapp/railpack-frontend"
const defaultRegistryInsecure = true
const defaultAuthMode = authModeDev
const defaultRuntimeEngine = runtimeEnginePodman
const defaultTraefikDynamicConfigDir = "/var/lib/ovek/traefik/dynamic"
const defaultTraefikBrainServiceURL = "http://brain:8081"
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
	}, nil
}
