package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func worktree(t *testing.T) launcher {
	t.Helper()
	root := t.TempDir()
	root, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Chdir(root)
	base := t.TempDir()
	t.Setenv("XDG_STATE_HOME", base)
	hash := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(hash[:])[:20]
	directory := filepath.Join(base, "ride-home-router", key)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		t.Fatal(err)
	}
	return launcher{root: root, key: key, directory: directory}
}

func TestStatusRequiresMatchingProcessIdentity(t *testing.T) {
	l := worktree(t)
	for _, tc := range []struct {
		name  string
		pid   int
		start string
	}{
		{name: "missing start", pid: os.Getpid()},
		{name: "stale start", pid: os.Getpid(), start: "not-this-process"},
		{name: "missing process", pid: 999999999, start: "old-start"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := l.save("state.json", environment{Root: l.root, PID: tc.pid, Start: tc.start}); err != nil {
				t.Fatal(err)
			}
			if err := l.save("ready.json", readiness{URL: "http://127.0.0.1:1", Login: "http://127.0.0.1:1"}); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := run(t.Context(), []string{"status"}, &output); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(output.String(), "Stopped\n") || strings.Contains(output.String(), "URL:") {
				t.Fatalf("stale readiness reported running: %s", output.String())
			}
		})
	}
}

func TestStatusReadsLegacyStateAndIdentityChooser(t *testing.T) {
	l := worktree(t)
	start, err := processStart(os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if err = l.save("state.json", environment{Root: l.root, PID: os.Getpid(), Start: start}); err != nil {
		t.Fatal(err)
	}
	if err = l.save("ready.json", readiness{Login: "http://127.0.0.1:1/__dev/?key=synthetic"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err = run(t.Context(), []string{"status"}, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(output.String(), "Running\n") || !strings.Contains(output.String(), "key=synthetic&choose=1 (admin / member / denied)") {
		t.Fatalf("incorrect running status: %s", output.String())
	}
}

func TestStatusAndStopWithoutEnvironmentNeedNoRuntime(t *testing.T) {
	l := worktree(t)
	t.Setenv("DEV_RUNTIME", "missing-runtime")
	for _, action := range []string{"status", "stop"} {
		var output bytes.Buffer
		if err := run(t.Context(), []string{action}, &output); err != nil {
			t.Fatal(err)
		}
		if output.String() != "No environment for this worktree\n" {
			t.Fatalf("unexpected status: %s", output.String())
		}
		if _, err := os.Stat(filepath.Join(l.directory, "state.json")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("created an environment for %s: %v", action, err)
		}
	}
}

func TestOtherWorktreeStateIsRejected(t *testing.T) {
	l := worktree(t)
	if err := l.save("state.json", environment{Root: l.root + "-other"}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"start", "status", "stop", "reset"} {
		var output bytes.Buffer
		err := run(t.Context(), []string{action}, &output)
		if err == nil || err.Error() != "state ownership mismatch" {
			t.Fatalf("%s accepted other worktree: %v", action, err)
		}
	}
}

func TestLifecycleLockCanBeCancelled(t *testing.T) {
	l := worktree(t)
	lock, err := os.OpenFile(filepath.Join(l.directory, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lock.Close() })
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	var output bytes.Buffer
	if err = run(ctx, []string{"status"}, &output); !errors.Is(err, context.Canceled) {
		t.Fatalf("lock ignored cancellation: %v", err)
	}
}

func TestStopAndResetRefuseUnownedContainers(t *testing.T) {
	l := worktree(t)
	runtime := filepath.Join(t.TempDir(), "runtime")
	logPath := filepath.Join(t.TempDir(), "commands")
	t.Setenv("LAUNCHER_TEST_COMMANDS", logPath)
	script := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> \"$LAUNCHER_TEST_COMMANDS\"\nprintf '%s\\n' '[{\"Config\":{\"Labels\":{\"rhr.dev.owner\":\"other\"}},\"State\":{\"Running\":true}}]'\n"
	//nolint:gosec // G306: the test runtime must be executable.
	if err := os.WriteFile(runtime, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := l.save("state.json", environment{Root: l.root, Owner: "ours", Runtime: runtime, Container: "owned-name"}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"stop", "reset"} {
		var output bytes.Buffer
		err := run(t.Context(), []string{action}, &output)
		if err == nil || !strings.Contains(err.Error(), "container ownership mismatch") {
			t.Fatalf("%s accepted an unowned container: %v", action, err)
		}
	}
	root, err := os.OpenRoot(filepath.Dir(logPath))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = root.Close() }()
	commands, err := root.ReadFile(filepath.Base(logPath))
	if err != nil {
		t.Fatal(err)
	}
	if string(commands) != "inspect\ninspect\n" {
		t.Fatalf("mutated unowned container: %s", commands)
	}
}

func TestStopPreservesLegacySecretsAndClearsStaleReadiness(t *testing.T) {
	l := worktree(t)
	runtime := filepath.Join(t.TempDir(), "runtime")
	script := "#!/bin/sh\nprintf '%s\\n' '[{\"Config\":{\"Labels\":{\"rhr.dev.owner\":\"ours\"}},\"State\":{\"Running\":false}}]'\n"
	//nolint:gosec // G306: the test runtime must be executable.
	if err := os.WriteFile(runtime, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	state := environment{Root: l.root, Owner: "ours", Runtime: runtime, Container: "owned-name", Password: "synthetic-password", EncryptionKey: "synthetic-key", Port: 1234, PID: os.Getpid(), Start: "not-this-process"}
	if err := l.save("state.json", state); err != nil {
		t.Fatal(err)
	}
	if err := l.save("ready.json", readiness{Login: "stale"}); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(t.Context(), []string{"stop"}, &output); err != nil {
		t.Fatal(err)
	}
	saved, found, err := readJSON[environment](l.directory, "state.json")
	if err != nil || !found {
		t.Fatalf("read saved state: %v", err)
	}
	state.PID, state.Start = 0, ""
	if saved != state {
		t.Fatalf("stop changed persistent identity: %+v", saved)
	}
	if _, err = os.Stat(filepath.Join(l.directory, "ready.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stale readiness survived stop: %v", err)
	}
	info, err := os.Stat(filepath.Join(l.directory, "state.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("state permissions: %v, %v", info, err)
	}
}

func TestRuntimeWarningsDoNotCorruptMachineReadableOutput(t *testing.T) {
	runtime := filepath.Join(t.TempDir(), "runtime")
	script := `#!/bin/sh
printf '%s\n' 'nonfatal runtime warning' >&2
if [ "$1" = inspect ]; then
    printf '%s\n' '[{"Config":{"Labels":{"rhr.dev.owner":"ours"}},"State":{"Running":true}}]'
else
    printf '%s\n' '127.0.0.1:5432'
fi
`
	//nolint:gosec // G306: the test runtime must be executable.
	if err := os.WriteFile(runtime, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	state := environment{Owner: "ours", Container: "owned-name", Runtime: runtime}
	info, found, err := inspect(t.Context(), &state)
	if err != nil || !found || !info.State.Running {
		t.Fatalf("inspect could not read stdout: %v, %v", info, err)
	}
	port, err := container(t.Context(), &state, "port", state.Container, "5432/tcp")
	if err != nil || string(port) != "127.0.0.1:5432\n" {
		t.Fatalf("port output contains stderr: %q, %v", port, err)
	}
}
