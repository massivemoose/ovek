package main

import "testing"

func TestLoadConfigRequiresAPIKey(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected an error when BRAIN_API_KEY is missing")
	}
}

func TestLoadConfigReadsAPIKey(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.BrainAPIKey != "test-key" {
		t.Fatalf("expected API key %q, got %q", "test-key", cfg.BrainAPIKey)
	}

	if cfg.DataDir != defaultDataDir {
		t.Fatalf("expected data dir %q, got %q", defaultDataDir, cfg.DataDir)
	}
	if cfg.RuntimeEngine != defaultRuntimeEngine {
		t.Fatalf("expected runtime engine %q, got %q", defaultRuntimeEngine, cfg.RuntimeEngine)
	}
	if cfg.RuntimeHost != defaultPodmanRuntimeHost {
		t.Fatalf("expected runtime host %q, got %q", defaultPodmanRuntimeHost, cfg.RuntimeHost)
	}
	if cfg.BuildKitHost != defaultBuildKitHost {
		t.Fatalf("expected BuildKit host %q, got %q", defaultBuildKitHost, cfg.BuildKitHost)
	}
	if cfg.BuildRegistryPublishHost != defaultBuildRegistryPublishHost {
		t.Fatalf("expected build registry publish host %q, got %q", defaultBuildRegistryPublishHost, cfg.BuildRegistryPublishHost)
	}
	if cfg.RuntimeRegistryHost != defaultRuntimeRegistryHost {
		t.Fatalf("expected runtime registry host %q, got %q", defaultRuntimeRegistryHost, cfg.RuntimeRegistryHost)
	}
	if cfg.RegistryAPIBaseURL != defaultRegistryAPIBaseURL {
		t.Fatalf("expected registry API base URL %q, got %q", defaultRegistryAPIBaseURL, cfg.RegistryAPIBaseURL)
	}
	if cfg.RailpackFrontendImage != defaultRailpackFrontendImage {
		t.Fatalf("expected Railpack frontend image %q, got %q", defaultRailpackFrontendImage, cfg.RailpackFrontendImage)
	}
	if cfg.RegistryInsecure != defaultRegistryInsecure {
		t.Fatalf("expected registry insecure %t, got %t", defaultRegistryInsecure, cfg.RegistryInsecure)
	}
	if cfg.ProjectsHostDataDir != defaultProjectsHostDataDir {
		t.Fatalf("expected projects host data dir %q, got %q", defaultProjectsHostDataDir, cfg.ProjectsHostDataDir)
	}
	if cfg.PocketBaseImage != defaultPocketBaseImage {
		t.Fatalf("expected PocketBase image %q, got %q", defaultPocketBaseImage, cfg.PocketBaseImage)
	}
	if cfg.TraefikDynamicConfigDir != defaultTraefikDynamicConfigDir {
		t.Fatalf("expected Traefik config dir %q, got %q", defaultTraefikDynamicConfigDir, cfg.TraefikDynamicConfigDir)
	}
	if cfg.TraefikBrainServiceURL != defaultTraefikBrainServiceURL {
		t.Fatalf("expected Traefik brain service URL %q, got %q", defaultTraefikBrainServiceURL, cfg.TraefikBrainServiceURL)
	}
	if cfg.AuthMode != authModeDev {
		t.Fatalf("expected auth mode %q, got %q", authModeDev, cfg.AuthMode)
	}
}

func TestLoadConfigReadsBuildKitHostOverride(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("BUILDKIT_HOST", "docker-container://custom-buildkit")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.BuildKitHost != "docker-container://custom-buildkit" {
		t.Fatalf("expected BuildKit host %q, got %q", "docker-container://custom-buildkit", cfg.BuildKitHost)
	}
}

func TestLoadConfigReadsRegistryAndProjectRuntimeOverrides(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("BUILD_REGISTRY_PUBLISH_HOST", "build-registry.internal:5000")
	t.Setenv("RUNTIME_REGISTRY_HOST", "runtime-registry.internal:5000")
	t.Setenv("REGISTRY_API_BASE_URL", "http://registry.internal:5000")
	t.Setenv("RAILPACK_FRONTEND_IMAGE", "ghcr.io/example/railpack-frontend:1.2.3")
	t.Setenv("REGISTRY_INSECURE", "false")
	t.Setenv("PROJECTS_HOST_DATA_DIR", "/srv/ovek/projects")
	t.Setenv("POCKETBASE_IMAGE", "custom/pocketbase:1.0")
	t.Setenv("TRAEFIK_DYNAMIC_CONFIG_DIR", "/srv/ovek/traefik")
	t.Setenv("TRAEFIK_BRAIN_SERVICE_URL", "http://brain.internal:8081")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected config to load, got error: %v", err)
	}

	if cfg.BuildRegistryPublishHost != "build-registry.internal:5000" {
		t.Fatalf("expected build registry publish host %q, got %q", "build-registry.internal:5000", cfg.BuildRegistryPublishHost)
	}
	if cfg.RuntimeRegistryHost != "runtime-registry.internal:5000" {
		t.Fatalf("expected runtime registry host %q, got %q", "runtime-registry.internal:5000", cfg.RuntimeRegistryHost)
	}
	if cfg.RegistryAPIBaseURL != "http://registry.internal:5000" {
		t.Fatalf("expected registry API base URL %q, got %q", "http://registry.internal:5000", cfg.RegistryAPIBaseURL)
	}
	if cfg.RailpackFrontendImage != "ghcr.io/example/railpack-frontend:1.2.3" {
		t.Fatalf("expected Railpack frontend image %q, got %q", "ghcr.io/example/railpack-frontend:1.2.3", cfg.RailpackFrontendImage)
	}
	if cfg.RegistryInsecure {
		t.Fatalf("expected registry insecure false, got %t", cfg.RegistryInsecure)
	}
	if cfg.ProjectsHostDataDir != "/srv/ovek/projects" {
		t.Fatalf("expected projects host data dir %q, got %q", "/srv/ovek/projects", cfg.ProjectsHostDataDir)
	}
	if cfg.PocketBaseImage != "custom/pocketbase:1.0" {
		t.Fatalf("expected PocketBase image %q, got %q", "custom/pocketbase:1.0", cfg.PocketBaseImage)
	}
	if cfg.TraefikDynamicConfigDir != "/srv/ovek/traefik" {
		t.Fatalf("expected Traefik config dir %q, got %q", "/srv/ovek/traefik", cfg.TraefikDynamicConfigDir)
	}
	if cfg.TraefikBrainServiceURL != "http://brain.internal:8081" {
		t.Fatalf("expected Traefik brain service URL %q, got %q", "http://brain.internal:8081", cfg.TraefikBrainServiceURL)
	}
}

func TestLoadConfigRejectsInvalidRegistryInsecureValue(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("REGISTRY_INSECURE", "definitely-not-a-bool")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected invalid REGISTRY_INSECURE to fail")
	}
}

func TestLoadConfigAllowsProdModeWithoutStaticAPIKey(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeProd)
	t.Setenv("BRAIN_API_KEY", "")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("expected prod config to load, got error: %v", err)
	}
	if cfg.AuthMode != authModeProd {
		t.Fatalf("expected auth mode %q, got %q", authModeProd, cfg.AuthMode)
	}
	if cfg.BrainAPIKey != "" {
		t.Fatalf("expected static api key to be empty in prod mode, got %q", cfg.BrainAPIKey)
	}
}

func TestLoadConfigRejectsDockerRuntime(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("RUNTIME_ENGINE", "docker")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected docker runtime to fail")
	}
}

func TestLoadConfigRejectsInvalidRuntimeEngine(t *testing.T) {
	t.Setenv("OVEK_AUTH_MODE", authModeDev)
	t.Setenv("BRAIN_API_KEY", "test-key")
	t.Setenv("RUNTIME_ENGINE", "containerd")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected invalid RUNTIME_ENGINE to fail")
	}
}
