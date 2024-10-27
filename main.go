package main

import (
	"bufio"
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
	alpine     = "alpine:latest"
	initialCmd = "sh"
	wsRoute    = "/terminal"
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

// handleWebSocket manages WebSocket connections and streams Docker container commands.
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

	containerID, err := startInteractiveContainer(ctx, cli)
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("error starting container: %v", err)))
		return
	}

	defer func() {
		stopAndRemoveContainer(ctx, cli, containerID)
	}()

	attachResp, err := cli.ContainerAttach(ctx, containerID,
		container.AttachOptions{
			Stream: true,
			Stdin:  true,
			Stdout: true,
			Stderr: true,
		})
	if err != nil {
		conn.WriteMessage(websocket.TextMessage, []byte(fmt.Sprintf("error attaching to container: %v", err)))
		return
	}
	defer attachResp.Close()

	go readContainerOutput(conn, attachResp.Reader)
	readWebSocketInput(conn, attachResp.Conn)
}

// startInteractiveContainer creates and starts a Docker container with an interactive shell.
func startInteractiveContainer(ctx context.Context, cli *client.Client) (string, error) {
	cfg := &container.Config{
		Image:     alpine,
		Cmd:       []string{initialCmd},
		Tty:       true,
		OpenStdin: true,
	}

	resp, err := cli.ContainerCreate(ctx, cfg, nil, nil, nil, "")
	if err != nil {
		return "", fmt.Errorf("error creating container: %w", err)
	}

	if err := cli.ContainerStart(ctx, resp.ID, container.StartOptions{}); err != nil {
		return "", fmt.Errorf("error starting container: %w", err)
	}
	return resp.ID, nil
}

// stopAndRemoveContainer stops and removes a Docker container.
func stopAndRemoveContainer(ctx context.Context, cli *client.Client, containerID string) {
	if err := cli.ContainerKill(ctx, containerID, "SIGKILL"); err != nil {
		log.Printf("failed to stop container %s: %v", containerID, err)
	}
	if err := cli.ContainerRemove(ctx, containerID, container.RemoveOptions{Force: true}); err != nil {
		log.Printf("failed to remove container %s: %v", containerID, err)
	}
}

// readContainerOutput reads output from the container and sends it over WebSocket.
func readContainerOutput(conn *websocket.Conn, reader io.Reader) {
	scanner := bufio.NewScanner(reader)
	for scanner.Scan() {
		if err := conn.WriteMessage(websocket.TextMessage, scanner.Bytes()); err != nil {
			log.Printf("error writing to WebSocket: %v", err)
			break
		}
	}
	if err := scanner.Err(); err != nil {
		log.Printf("error reading container output: %v", err)
	}
}

// readWebSocketInput reads input from WebSocket and sends it to the container.
func readWebSocketInput(conn *websocket.Conn, containerStdin io.WriteCloser) {
	defer containerStdin.Close()
	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			log.Printf("websocket read error: %v", err)
			break
		}
		_, err = containerStdin.Write(append(message, '\n'))
		if err != nil {
			log.Printf("error writing to container stdin: %v", err)
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
