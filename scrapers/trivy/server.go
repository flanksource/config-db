package trivy

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"sync"
	"time"

	"github.com/samber/lo"
)

var (
	trivyServerMutex sync.Mutex
	DefaultPort      = "4954"
)

// startTrivyServer starts a Trivy server if it's not already running
func startTrivyServer(ctx context.Context, port string) error {
	trivyServerMutex.Lock()
	defer trivyServerMutex.Unlock()

	port = lo.CoalesceOrEmpty(port, DefaultPort)

	// Check if already running
	if isPortInUse(port) {
		return nil
	}

	trivyServerCmd := exec.CommandContext(ctx, "trivy", "server", "--listen", "127.0.0.0:"+port)

	if err := trivyServerCmd.Start(); err != nil {
		return fmt.Errorf("failed to start trivy server: %w", err)
	}

	// Wait for server to be ready (or fail)
	if err := waitForServerReadyOrFail(trivyServerCmd, port); err != nil {
		stopTrivyServer(trivyServerCmd)
		return fmt.Errorf("trivy server failed to start: %w", err)
	}

	return nil
}

func stopTrivyServer(trivyServerCmd *exec.Cmd) error {
	trivyServerMutex.Lock()
	defer trivyServerMutex.Unlock()

	if err := trivyServerCmd.Process.Kill(); err != nil {
		return fmt.Errorf("failed to kill trivy server: %w", err)
	}

	return nil
}

func isPortInUse(port string) bool {
	conn, err := net.DialTimeout("tcp", "localhost:"+port, 1*time.Second)
	defer conn.Close() //nolint:errcheck
	if err != nil {
		return false
	}
	return true
}

func waitForServerReadyOrFail(cmd *exec.Cmd, port string) error {
	interval := 500 * time.Millisecond
	timeout := 30 * time.Second
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		if cmd != nil && cmd.ProcessState != nil && cmd.ProcessState.Exited() {
			return fmt.Errorf("trivy server process exited prematurely")
		}

		time.Sleep(interval)

		// Check if server is ready
		if isPortInUse(port) {
			return nil
		}
	}

	return fmt.Errorf("server failed to start within %v", timeout)
}
