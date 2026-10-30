package nettrace

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// installedRuntimes lists the major versions of Microsoft.NETCore.App that `dotnet` can run.
func installedRuntimes(ctx context.Context, t *testing.T) map[string]bool {
	t.Helper()
	out, err := exec.CommandContext(ctx, "dotnet", "--list-runtimes").Output()
	if err != nil {
		t.Fatal(err)
	}
	majors := map[string]bool{}
	for line := range strings.SplitSeq(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "Microsoft.NETCore.App" {
			major, _, _ := strings.Cut(fields[1], ".")
			majors[major] = true
		}
	}
	return majors
}

func runDotnet(ctx context.Context, t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "dotnet", args...) //nolint:gosec // G204: arguments are this file's literals and t.TempDir() paths
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "DOTNET_NOLOGO=1", "DOTNET_CLI_TELEMETRY_OPTOUT=1")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("dotnet %v: %v\n%s", args, err, out)
	}
}

// TestLive samples a real .NET process end to end on every installed runtime among the project's targets. The
// project is built in a temporary folder, because building in place would record the checkout path in the trace.
func TestLive(t *testing.T) {
	if testing.Short() {
		t.Skip("-short skips the live .NET test")
	}
	if _, err := exec.LookPath("dotnet"); err != nil {
		t.Skip("dotnet is not on PATH")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	runtimes := installedRuntimes(ctx, t)
	project := t.TempDir()
	if err := os.CopyFS(project, os.DirFS(filepath.Join("testdata", "spin"))); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"net6.0", "net8.0", "net9.0", "net10.0"} {
		major := strings.TrimSuffix(strings.TrimPrefix(target, "net"), ".0")
		if !runtimes[major] {
			t.Logf("%s: runtime not installed", target)
			continue
		}
		t.Run(target, func(t *testing.T) {
			out := filepath.Join(project, "out-"+target)
			runDotnet(ctx, t, project, "build", "-c", "Release", "-f", target, "-o", out)
			liveTarget(ctx, t, target, filepath.Join(out, "Spin.dll"))
		})
	}
}

func liveTarget(ctx context.Context, t *testing.T, target, dll string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, "dotnet", dll) //nolint:gosec // G204: dll is the test's own build output
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = stdin.Close(); _ = cmd.Wait() })

	var stream bytes.Buffer
	var session *Session
	deadline := time.Now().Add(20 * time.Second)
	for session == nil {
		session, err = StartTracing(ctx, cmd.Process.Pid, Config{}, &stream)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrNoEndpoint) || time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	// A fixed sleep cannot cover a loaded runner's startup plus the work, so the session stops only once Spin has
	// printed its result.
	if _, err := io.WriteString(stdin, "\n"); err != nil {
		t.Fatal(err)
	}
	if _, err := bufio.NewReader(stdout).ReadString('\n'); err != nil {
		t.Fatal(err)
	}
	stopCtx, stopCancel := context.WithTimeout(ctx, 30*time.Second)
	defer stopCancel()
	if err := session.Stop(stopCtx); err != nil {
		t.Fatal(err)
	}
	_ = stdin.Close()

	if record := os.Getenv("NETTRACE_RECORD"); record != "" && target == "net8.0" {
		if err := os.WriteFile(record, stream.Bytes(), 0o600); err != nil { //nolint:gosec // G703: the developer names this path to re-record the fixture
			t.Fatal(err)
		}
	}
	trace, err := Parse(io.Reader(&stream))
	if err != nil {
		t.Fatal(err)
	}
	var alpha, beta int
	for _, sample := range trace.Samples {
		frame, ok := innermostIn(sample, "Spin")
		switch {
		case !ok:
		case strings.HasSuffix(frame.Method, "Alpha"):
			alpha++
		case strings.HasSuffix(frame.Method, "Beta"):
			beta++
		}
	}
	if alpha == 0 || beta == 0 || alpha < beta {
		t.Fatalf("%s: alpha = %d, beta = %d samples, want both and alpha ahead (alpha ran twice as long)", target, alpha, beta)
	}
}
