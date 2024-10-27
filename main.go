package main

import (
	"context"
	"fmt"
	"log"
	"net/http"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/gorilla/websocket"
)

const (
	alpine  = "alpine:latest"
	logCmd  = "while true; do echo $(date); sleep 1; done"
	wsRoute = "/logs"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

func main() {
	ctx := context.Background()
	cli, err := initDockerClient(ctx)
	if err != nil {
		log.Fatalf("error connecting Docker engine: %v", err)
	}
	defer cli.Close()

	http.HandleFunc(wsRoute, func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(ctx, cli, w, r)
	})

	fmt.Println("websocket server starting on ws://localhost:8080/logs")
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// handleWebSocket handles the WebSocket connection and streams container logs.
func handleWebSocket(ctx context.Context, cli *client.Client, w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("failed to upgrade websocket: %v", err)
		return
	}
	defer conn.Close()

	resp, err := runLogContainer(ctx, cli)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("error: %v", err)))
		return
	}

	logs, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, Follow: true})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("failed to get logs: %v", err)))
		return
	}
	defer logs.Close()

	// Stream logs to WebSocket
	buf := make([]byte, 1024)
	for {
		n, err := logs.Read(buf)
		if err != nil {
			break
		}
		conn.WriteMessage(websocket.TextMessage, buf[:n])
	}

	cli.ContainerKill(ctx, resp.ID, "SIGKILL")
}

// runLogContainer starts a Docker container that outputs periodic logs.
func runLogContainer(ctx context.Context, cli *client.Client) (container.CreateResponse, error) {
	cfg := &container.Config{
		Image: alpine,
		Cmd:   []string{"sh", "-c", logCmd},
		Tty:   true,
	}

	resp, err := cli.ContainerCreate(ctx, cfg, nil, nil, nil, "")
	if err != nil {
		return container.CreateResponse{}, fmt.Errorf("error creating container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return container.CreateResponse{}, fmt.Errorf("error starting container: %w", err)
	}
	return resp, nil
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
