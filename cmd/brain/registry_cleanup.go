package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

var errUnmanagedImageRef = errors.New("image ref is not managed by the runtime registry")
var errRegistryManifestNotFound = errors.New("registry manifest not found")

var registryManifestAcceptHeader = strings.Join([]string{
	"application/vnd.docker.distribution.manifest.v2+json",
	"application/vnd.docker.distribution.manifest.list.v2+json",
	"application/vnd.oci.image.manifest.v1+json",
	"application/vnd.oci.image.index.v1+json",
}, ", ")

type registryArtifactCleaner interface {
	CleanupImage(ctx context.Context, imageRef string) error
}

type registryHTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type registryArtifactCleanerClient struct {
	runtimeRegistryHost string
	registryAPIBaseURL  string
	client              registryHTTPClient
}

type taggedImageRef struct {
	Host       string
	Repository string
	Tag        string
}

func newRegistryArtifactCleaner(runtimeRegistryHost string, registryAPIBaseURL string, client registryHTTPClient) registryArtifactCleanerClient {
	if client == nil {
		client = &http.Client{}
	}

	return registryArtifactCleanerClient{
		runtimeRegistryHost: strings.Trim(strings.TrimSpace(runtimeRegistryHost), "/"),
		registryAPIBaseURL:  strings.TrimRight(strings.TrimSpace(registryAPIBaseURL), "/"),
		client:              client,
	}
}

func (cleaner registryArtifactCleanerClient) CleanupImage(ctx context.Context, imageRef string) error {
	ref, err := parseTaggedImageRef(imageRef)
	if err != nil {
		return err
	}
	if ref.Host != cleaner.runtimeRegistryHost {
		return errUnmanagedImageRef
	}

	digest, err := cleaner.resolveManifestDigest(ctx, ref.Repository, ref.Tag)
	if err != nil {
		return err
	}

	return cleaner.deleteManifest(ctx, ref.Repository, digest)
}

func parseTaggedImageRef(imageRef string) (taggedImageRef, error) {
	imageRef = strings.TrimSpace(imageRef)
	if imageRef == "" {
		return taggedImageRef{}, errors.New("image ref is required")
	}

	firstSlash := strings.Index(imageRef, "/")
	if firstSlash <= 0 || firstSlash == len(imageRef)-1 {
		return taggedImageRef{}, fmt.Errorf("image ref %q must include a registry host and repository", imageRef)
	}

	host := imageRef[:firstSlash]
	remainder := imageRef[firstSlash+1:]
	lastSlash := strings.LastIndex(remainder, "/")
	tagSeparator := strings.LastIndex(remainder, ":")
	if tagSeparator <= lastSlash || tagSeparator == len(remainder)-1 {
		return taggedImageRef{}, fmt.Errorf("image ref %q must include a tag", imageRef)
	}

	repository := remainder[:tagSeparator]
	tag := remainder[tagSeparator+1:]
	if repository == "" || tag == "" {
		return taggedImageRef{}, fmt.Errorf("image ref %q is invalid", imageRef)
	}

	return taggedImageRef{
		Host:       host,
		Repository: repository,
		Tag:        tag,
	}, nil
}

func (cleaner registryArtifactCleanerClient) resolveManifestDigest(ctx context.Context, repository string, tag string) (string, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, cleaner.registryManifestURL(repository, tag), nil)
	if err != nil {
		return "", fmt.Errorf("create registry manifest request: %w", err)
	}
	request.Header.Set("Accept", registryManifestAcceptHeader)

	response, err := cleaner.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("fetch registry manifest for %s:%s: %w", repository, tag, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", errRegistryManifestNotFound
	default:
		return "", fmt.Errorf("fetch registry manifest for %s:%s: unexpected status %d", repository, tag, response.StatusCode)
	}

	digest := strings.TrimSpace(response.Header.Get("Docker-Content-Digest"))
	if digest == "" {
		return "", fmt.Errorf("fetch registry manifest for %s:%s: missing Docker-Content-Digest header", repository, tag)
	}

	return digest, nil
}

func (cleaner registryArtifactCleanerClient) deleteManifest(ctx context.Context, repository string, digest string) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodDelete, cleaner.registryManifestURL(repository, digest), nil)
	if err != nil {
		return fmt.Errorf("create registry manifest delete request: %w", err)
	}

	response, err := cleaner.client.Do(request)
	if err != nil {
		return fmt.Errorf("delete registry manifest %s@%s: %w", repository, digest, err)
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)

	switch response.StatusCode {
	case http.StatusAccepted:
		return nil
	case http.StatusNotFound:
		return errRegistryManifestNotFound
	default:
		return fmt.Errorf("delete registry manifest %s@%s: unexpected status %d", repository, digest, response.StatusCode)
	}
}

func (cleaner registryArtifactCleanerClient) registryManifestURL(repository string, reference string) string {
	return cleaner.registryAPIBaseURL + "/v2/" + registryRepositoryPath(repository) + "/manifests/" + url.PathEscape(reference)
}

func registryRepositoryPath(repository string) string {
	parts := strings.Split(repository, "/")
	for i, part := range parts {
		parts[i] = url.PathEscape(part)
	}

	return strings.Join(parts, "/")
}

func listProjectDeploymentImageRefs(db *sql.DB, projectName string) ([]string, error) {
	rows, err := db.Query(
		`SELECT image_ref
		 FROM deployments
		 WHERE project_name = ?
		 ORDER BY created_at ASC`,
		projectName,
	)
	if err != nil {
		return nil, fmt.Errorf("list deployment image refs for project %q: %w", projectName, err)
	}
	defer rows.Close()

	var imageRefs []string
	for rows.Next() {
		var imageRef string
		if err := rows.Scan(&imageRef); err != nil {
			return nil, fmt.Errorf("scan deployment image ref for project %q: %w", projectName, err)
		}
		imageRefs = append(imageRefs, imageRef)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate deployment image refs for project %q: %w", projectName, err)
	}

	return imageRefs, nil
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(values))
	deduped := make([]string, 0, len(values))
	for _, value := range values {
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		deduped = append(deduped, value)
	}

	return deduped
}
