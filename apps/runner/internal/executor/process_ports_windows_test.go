//go:build windows

package executor

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestProcessOwnedPortChild(t *testing.T) {
	if os.Getenv("RUNNER_PORT_OWNER_CHILD") != "1" {
		t.Skip("native port authority fixture")
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	fmt.Println(listener.Addr().String())
	_, _ = bufio.NewReader(os.Stdin).ReadByte()
}

func TestProcessPortAuthorityRealWindowsJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestProcessOwnedPortChild$")
	hideWindow(cmd)
	cmd.Env = append(os.Environ(), "RUNNER_PORT_OWNER_CHILD=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	owner, err := startPortOwnedProcess(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { owner.close(); _ = input.Close(); _ = cmd.Wait() }()
	scanner := bufio.NewScanner(output)
	if !scanner.Scan() {
		t.Fatal("native child did not listen")
	}
	_, value, err := net.SplitHostPort(strings.TrimSpace(scanner.Text()))
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(value, 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	controller := new(ProcessControl)
	release := controller.bindOwned(cmd.Process, owner)
	defer release()
	address, err := controller.OwnedLoopbackAddress(ctx, uint16(port))
	if err != nil || address != net.JoinHostPort("127.0.0.1", value) {
		t.Fatalf("owned listener denied: address=%q error=%v", address, err)
	}
	unrelated, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer unrelated.Close()
	ports, err := controller.OwnedLoopbackPorts(ctx)
	if err != nil || !slices.Contains(ports, int(port)) || slices.Contains(ports, unrelated.Addr().(*net.TCPAddr).Port) {
		t.Fatalf("owned port discovery: ports=%v err=%v", ports, err)
	}
	if _, err := controller.OwnedLoopbackAddress(ctx, uint16(unrelated.Addr().(*net.TCPAddr).Port)); err == nil {
		t.Fatal("unrelated listener admitted")
	}
	release()
	if _, err := controller.OwnedLoopbackAddress(ctx, uint16(port)); err == nil {
		t.Fatal("expired controller admitted listener")
	}
}
