package trivy

import (
	"bytes"
	"context"
	"os/exec"

	"github.com/flanksource/commons/logger"
)

func runCommand(ctx context.Context, command string, args []string) ([]byte, error) {
	logger.Infof("Running command: %s %s", command, args)

	cmd := exec.CommandContext(ctx, command, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	stdout, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	return stdout, nil
}
