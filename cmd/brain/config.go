package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
)

const defaultDataDir = "/var/lib/alces"
const defaultBuildKitHost = "docker-container://buildkit"
const defaultProjectsHostDataDir = "/var/lib/alces/projects"
const defaultPocketBaseImage = "elestio/pocketbase:latest"
const defaultBuildRegistryPublishHost = "host.docker.internal:5001"
const defaultRuntimeRegistryHost = "localhost:5001"
const defaultRegistryAPIBaseURL = "http://registry:5000"
const defaultRailpackFrontendImage = "ghcr.io/railwayapp/railpack-frontend"
const defaultRegistryInsecure = true
const defaultAuthMode = authModeDev

type config struct {
	AuthMode                 string
	BrainAPIKey              string
	DataDir                  string
	BuildKitHost             string
	BuildRegistryPublishHost string
	RuntimeRegistryHost      string
	RegistryAPIBaseURL       string
	RailpackFrontendImage    string
	RegistryInsecure         bool
	ProjectsHostDataDir      string
	PocketBaseImage          string
}

func loadConfig() (config, error) {
	authMode := strings.TrimSpace(os.Getenv("ALCES_AUTH_MODE"))
	if authMode == "" {
		authMode = defaultAuthMode
	}
	if authMode != authModeDev && authMode != authModeProd {
		return config{}, errors.New("ALCES_AUTH_MODE must be dev or prod")
	}

	apiKey := strings.TrimSpace(os.Getenv("BRAIN_API_KEY"))
	if authMode == authModeDev && apiKey == "" {
		return config{}, errors.New("BRAIN_API_KEY is required in dev auth mode")
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

	return config{
		AuthMode:                 authMode,
		BrainAPIKey:              apiKey,
		DataDir:                  defaultDataDir,
		BuildKitHost:             buildKitHost,
		BuildRegistryPublishHost: buildRegistryPublishHost,
		RuntimeRegistryHost:      runtimeRegistryHost,
		RegistryAPIBaseURL:       registryAPIBaseURL,
		RailpackFrontendImage:    railpackFrontendImage,
		RegistryInsecure:         registryInsecure,
		ProjectsHostDataDir:      projectsHostDataDir,
		PocketBaseImage:          pocketBaseImage,
	}, nil
}
