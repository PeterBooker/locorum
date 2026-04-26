package docker

import (
	"bytes"
	"fmt"
	"io"

	"github.com/docker/docker/pkg/stdcopy"
)

// readDemuxedLogs reads a Docker log stream — which is a multiplexed
// stdout/stderr frame — and returns the human-readable concatenation. Used
// by ContainerLogs when the container was created with Tty:false.
func readDemuxedLogs(r io.Reader) (string, error) {
	var stdout, stderr bytes.Buffer
	if _, err := stdcopy.StdCopy(&stdout, &stderr, r); err != nil {
		return "", fmt.Errorf("demux logs: %w", err)
	}
	if stderr.Len() == 0 {
		return stdout.String(), nil
	}
	if stdout.Len() == 0 {
		return stderr.String(), nil
	}
	return stdout.String() + stderr.String(), nil
}

// readRawLogs reads a Docker log stream from a Tty:true container. Such
// streams are NOT multiplexed: applying stdcopy demux to them produces
// garbage. Plain io.Copy is the right reader.
func readRawLogs(r io.Reader) (string, error) {
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		return "", fmt.Errorf("read logs: %w", err)
	}
	return buf.String(), nil
}
