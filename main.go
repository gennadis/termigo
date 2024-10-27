package main

import (
	"context"
	"fmt"
	"log"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

func main() {
	ctx := context.Background()
	apiClient, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		log.Fatalf("failed to init API client: %v", err)
	}
	defer apiClient.Close()

	apiClient.NegotiateAPIVersion(ctx)

	containers, err := apiClient.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		log.Fatalf("failed to get containter list: %v", err)
	}

	for i, ctr := range containers {
		fmt.Printf("%d: image: %s, status: %s\n", i+1, ctr.Image, ctr.Status)
	}
}
