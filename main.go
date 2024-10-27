package main

import (
	"context"
	"fmt"
	"log"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
)

const (
	alpine = "alpine:latest"
	shell  = "./bin/sh"
)

func main() {
	ctx := context.Background()

	cli, err := initDockerClient(ctx)
	if err != nil {
		log.Fatalf("error connecting Docker engine: %v", err)
	}
	defer cli.Close()

	cli.NegotiateAPIVersion(ctx)

	cfg := &container.Config{
		Image: alpine,
		Tty:   true,
		Cmd:   []string{shell},
	}

	resp, err := cli.ContainerCreate(ctx, cfg, nil, nil, nil, "")
	if err != nil {
		log.Fatalf("error creating %s container: %v", alpine, err)
	}

	cli.ContainerStart(ctx, resp.ID, container.StartOptions{})
	fmt.Printf("conrainer with image %q started: id %s\n", alpine, resp.ID)

	cli.ContainerKill(ctx, resp.ID, "SIGKILL")
	fmt.Printf("conrainer %s stopped\n", resp.ID)
}

// initDockerClient sets up and returns a Docker client.
func initDockerClient(ctx context.Context) (*client.Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}
	c.NegotiateAPIVersion(ctx)
	return c, nil
}
