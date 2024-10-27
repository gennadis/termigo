package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/gorilla/websocket"
)

const (
	alpine  = "alpine:latest"
	logCmd  = "while true; do echo $(date); sleep 1; done"
	wsRoute = "/logs"
)

var (
	upgrader = websocket.Upgrader{
		ReadBufferSize:  1024,
		WriteBufferSize: 1024,
		CheckOrigin:     func(r *http.Request) bool { return true },
	}
	maxConnections = 3
	activeSessions = sync.WaitGroup{}
)

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cli, err := initDockerClient(ctx)
	if err != nil {
		log.Fatalf("error connecting Docker engine: %v", err)
	}
	defer cli.Close()

	http.HandleFunc(wsRoute, func(w http.ResponseWriter, r *http.Request) {
		handleWebSocket(ctx, cli, w, r)
	})

	server := &http.Server{Addr: ":8080", Handler: nil}
	go func() {
		fmt.Println("websocket server starting on ws://localhost:8080" + wsRoute)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	fmt.Println("shutting down server")
	cancel()

	if err := server.Shutdown(ctx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}
	activeSessions.Wait()
	fmt.Println("server exited cleanly")
}

// handleWebSocket manages WebSocket connections and streams Docker container logs.
func handleWebSocket(ctx context.Context, cli *client.Client, w http.ResponseWriter, r *http.Request) {
	if maxConnections <= 0 {
		http.Error(w, "server is busy, please try again later", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("failed to upgrade websocket: %v", err)
		return
	}
	defer conn.Close()

	maxConnections--
	defer func() { maxConnections++ }()

	activeSessions.Add(1)
	defer activeSessions.Done()

	resp, err := runLogContainer(ctx, cli)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("error starting container: %v", err)))
		return
	}

	// Ensure container cleanup
	defer func() {
		if err := cli.ContainerKill(ctx, resp.ID, "SIGKILL"); err != nil {
			log.Printf("failed to stop container %s: %v", resp.ID, err)
		}
		if err := cli.ContainerRemove(ctx, resp.ID, container.RemoveOptions{Force: true}); err != nil {
			log.Printf("failed to remove container %s: %v", resp.ID, err)
		}
	}()

	logs, err := cli.ContainerLogs(ctx, resp.ID, container.LogsOptions{ShowStdout: true, Follow: true})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("failed to get logs: %v", err)))
		return
	}
	defer logs.Close()

	streamLogs(conn, logs)
}

// runLogContainer creates and starts a Docker container that runs a continuous logging command.
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

// streamLogs continuously streams logs from Docker to WebSocket
func streamLogs(conn *websocket.Conn, logs io.Reader) {
	buf := make([]byte, 1024)
	for {
		n, err := logs.Read(buf)
		if err != nil {
			conn.WriteMessage(websocket.TextMessage, []byte("error reading logs: "+err.Error()))
			break
		}
		if err := conn.WriteMessage(websocket.TextMessage, buf[:n]); err != nil {
			log.Printf("error writing to websocket: %v", err)
			break
		}
	}
}

// initDockerClient initializes and returns a Docker client.
func initDockerClient(ctx context.Context) (*client.Client, error) {
	c, err := client.NewClientWithOpts(client.FromEnv)
	if err != nil {
		return nil, err
	}
	c.NegotiateAPIVersion(ctx)
	return c, nil
}
