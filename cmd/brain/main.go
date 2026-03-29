package main

import (
	"context"
	"log"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/network"
	"github.com/docker/docker/client"
)

func main() {
	ctx := context.Background()
	cli, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		log.Fatalf("Failed to create docker client: %v", err)
	}

	// 1. Setup unique identification for this deployment
	currentTime := time.Now()
	timeString := currentTime.Format("150405")
	appName := "user-app-" + timeString

	// 2. Define the "New Idea" container
	config := &container.Config{
		Image: "nginxdemos/hello",
		Labels: map[string]string{
			"traefik.enable": "true",
			"traefik.http.routers." + appName + ".entrypoints":               "web",
			"traefik.http.routers." + appName + ".service":                   appName,
			"traefik.http.routers." + appName + ".rule":                      "Host(`" + appName + ".localhost`)",
			"traefik.http.services." + appName + ".loadbalancer.server.port": "80",
			"traefik.docker.network":                                         "alces-net",
			"com.docker.compose.project":                                     "alces",
		},
	}

	// 3. Define the Networking
	networkingConfig := &network.NetworkingConfig{
		EndpointsConfig: map[string]*network.EndpointSettings{
			"alces-net": {},
		},
	}

	// 4. Create the container
	resp, err := cli.ContainerCreate(ctx, config, nil, networkingConfig, nil, appName)
	if err != nil {
		log.Fatalf("Failed to create container: %v", err)
	}

	// 5. Start the container
	err = cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
	if err != nil {
		log.Fatalf("Failed to start container: %v", err)
	}
	log.Printf("Container started with url http://%s.localhost", appName)
	log.Printf("Container started with ID: %s", resp.ID)
}
