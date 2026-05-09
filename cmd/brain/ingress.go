package main

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const traefikWebEntrypoint = "web"
const brainIngressFileName = "brain.yaml"

type projectIngressManager interface {
	EnsureBrainRoute() error
	SyncProject(ctx context.Context, projectName string) error
	SyncAll(ctx context.Context) error
	RemoveProject(projectName string) error
}

type appTargetResolver interface {
	ResolveProjectAppReadinessTarget(ctx context.Context, projectName string, deploymentID string) (appReadinessTarget, error)
}

type traefikFileIngress struct {
	db              *sql.DB
	runtime         appTargetResolver
	configDir       string
	brainServiceURL string
}

func newTraefikFileIngress(db *sql.DB, runtime appTargetResolver, configDir string, brainServiceURL string) traefikFileIngress {
	configDir = strings.TrimSpace(configDir)
	if configDir == "" {
		configDir = defaultTraefikDynamicConfigDir
	}
	brainServiceURL = strings.TrimSpace(brainServiceURL)
	if brainServiceURL == "" {
		brainServiceURL = defaultTraefikBrainServiceURL
	}

	return traefikFileIngress{
		db:              db,
		runtime:         runtime,
		configDir:       configDir,
		brainServiceURL: brainServiceURL,
	}
}

func (ingress traefikFileIngress) EnsureBrainRoute() error {
	return ingress.writeConfig(brainIngressFileName, renderTraefikRouteConfig("brain", "brain.localhost", ingress.brainServiceURL))
}

func (ingress traefikFileIngress) SyncProject(ctx context.Context, projectName string) error {
	currentDeployment, found, err := getProjectCurrentDeployment(ingress.db, projectName)
	if err != nil {
		return fmt.Errorf("load current deployment for project %q ingress: %w", projectName, err)
	}
	if !found {
		return ingress.RemoveProject(projectName)
	}

	target, err := ingress.runtime.ResolveProjectAppReadinessTarget(ctx, projectName, currentDeployment.ID)
	if err != nil {
		_ = ingress.RemoveProject(projectName)
		return fmt.Errorf("resolve app target for project %q ingress: %w", projectName, err)
	}

	return ingress.writeConfig(
		projectIngressFileName(projectName),
		renderTraefikRouteConfig(projectRouteName(projectName), projectName+".localhost", target.URL),
	)
}

func (ingress traefikFileIngress) SyncAll(ctx context.Context) error {
	if err := os.MkdirAll(ingress.configDir, 0o755); err != nil {
		return fmt.Errorf("create Traefik config dir %q: %w", ingress.configDir, err)
	}
	if err := ingress.EnsureBrainRoute(); err != nil {
		return err
	}

	projectNames, err := listProjectNames(ingress.db)
	if err != nil {
		return err
	}

	var firstErr error
	activeFiles := make(map[string]struct{}, len(projectNames)+1)
	activeFiles[brainIngressFileName] = struct{}{}
	for _, projectName := range projectNames {
		if err := ingress.SyncProject(ctx, projectName); err != nil {
			if firstErr == nil {
				firstErr = err
			}
		}
		activeFiles[projectIngressFileName(projectName)] = struct{}{}
	}

	entries, err := os.ReadDir(ingress.configDir)
	if err != nil {
		return fmt.Errorf("read Traefik config dir %q: %w", ingress.configDir, err)
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if filepath.Ext(name) != ".yaml" {
			continue
		}
		if _, ok := activeFiles[name]; ok {
			continue
		}
		if err := os.Remove(filepath.Join(ingress.configDir, name)); err != nil && !os.IsNotExist(err) {
			if firstErr == nil {
				firstErr = fmt.Errorf("remove stale Traefik config %q: %w", name, err)
			}
		}
	}

	return firstErr
}

func (ingress traefikFileIngress) RemoveProject(projectName string) error {
	path := filepath.Join(ingress.configDir, projectIngressFileName(projectName))
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove project Traefik config for %q: %w", projectName, err)
	}

	return nil
}

func (ingress traefikFileIngress) writeConfig(fileName string, contents string) error {
	if err := os.MkdirAll(ingress.configDir, 0o755); err != nil {
		return fmt.Errorf("create Traefik config dir %q: %w", ingress.configDir, err)
	}

	path := filepath.Join(ingress.configDir, fileName)
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, []byte(contents), 0o644); err != nil {
		return fmt.Errorf("write Traefik config %q: %w", path, err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return fmt.Errorf("rename Traefik config %q: %w", path, err)
	}

	return nil
}

func projectIngressFileName(projectName string) string {
	return "project-" + projectName + ".yaml"
}

func projectRouteName(projectName string) string {
	return "app-" + projectName
}

func renderTraefikRouteConfig(routeName string, hostRule string, serviceURL string) string {
	lines := []string{
		"http:",
		"  routers:",
		"    " + routeName + ":",
		"      entryPoints:",
		"        - " + traefikWebEntrypoint,
		"      rule: Host(`" + hostRule + "`)",
		"      service: " + routeName,
		"  services:",
		"    " + routeName + ":",
		"      loadBalancer:",
		"        servers:",
		"          - url: " + serviceURL,
	}

	return strings.Join(lines, "\n") + "\n"
}
