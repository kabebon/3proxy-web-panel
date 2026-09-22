package proxy

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"
)

// dockerClient returns an http.Client dialing the Docker Unix socket.
func dockerClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				return net.Dial("unix", "/var/run/docker.sock")
			},
		},
		Timeout: 10 * time.Second,
	}
}

// ReloadProxy sends SIGHUP to the 3proxy container via Docker API.
func ReloadProxy(containerName string) error {
	client := dockerClient()

	url := fmt.Sprintf("http://localhost/containers/%s/kill?signal=HUP", containerName)
	req, err := http.NewRequest("POST", url, nil)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("docker api: %w", err)
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) //nolint:errcheck

	if resp.StatusCode >= 400 {
		return fmt.Errorf("docker returned status %d", resp.StatusCode)
	}

	return nil
}

// IsProxyRunning checks if the 3proxy container is running via Docker API.
func IsProxyRunning(containerName string) bool {
	client := dockerClient()

	url := fmt.Sprintf("http://localhost/containers/%s/json", containerName)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return false
	}

	resp, err := client.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.ReadAll(resp.Body) //nolint:errcheck

	return resp.StatusCode == 200
}
