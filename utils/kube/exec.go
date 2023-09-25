package kube

import (
	"bytes"
	"context"
	"fmt"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
)

// ExecutePodf runs the specified shell command inside a container of the specified pod
func ExecutePodf(ctx context.Context, client kubernetes.Interface, rc *rest.Config, namespace, pod, container string, command ...string) (string, string, error) {
	const tty = false
	req := client.CoreV1().RESTClient().Post().
		Resource("pods").
		Name(pod).
		Namespace(namespace).
		SubResource("exec").
		Param("container", container).
		Param("stdin", fmt.Sprintf("%t", false)).
		Param("stdout", fmt.Sprintf("%t", true)).
		Param("stderr", fmt.Sprintf("%t", true)).
		Param("tty", fmt.Sprintf("%t", tty))

	for _, c := range command {
		req.Param("command", c)
	}

	exec, err := remotecommand.NewSPDYExecutor(rc, "POST", req.URL())
	if err != nil {
		return "", "", fmt.Errorf("ExecutePodf: Failed to get SPDY Executor: %v", err)
	}
	var stdout, stderr bytes.Buffer
	err = exec.StreamWithContext(ctx, remotecommand.StreamOptions{
		Stdin:  nil,
		Stdout: &stdout,
		Stderr: &stderr,
		Tty:    tty,
	})

	_stdout := safeString(&stdout)
	_stderr := safeString(&stderr)

	if err != nil {
		return "", "", fmt.Errorf("failed to execute command: %v, stdout=%s stderr=%s", err, _stdout, _stderr)
	}

	return _stdout, _stderr, nil
}

func safeString(buf *bytes.Buffer) string {
	if buf == nil || buf.Len() == 0 {
		return ""
	}
	return buf.String()
}
