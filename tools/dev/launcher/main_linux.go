package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type environment struct {
	Root          string `json:"root"`
	Owner         string `json:"owner"`
	Runtime       string `json:"runtime"`
	Container     string `json:"container"`
	Password      string `json:"password"`
	EncryptionKey string `json:"encryption_key"`
	PID           int    `json:"pid,omitempty"`
	Start         string `json:"start,omitempty"`
	Port          int    `json:"port,omitempty"`
}

type readiness struct {
	URL   string `json:"url"`
	Login string `json:"login"`
}

type launcher struct {
	root, key, directory string
	output               io.Writer
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) != 1 || args[0] != "start" && args[0] != "status" && args[0] != "stop" && args[0] != "reset" {
		return errors.New("expected start, status, stop, or reset")
	}
	root, err := os.Getwd()
	if err != nil {
		return err
	}
	root, err = filepath.EvalSymlinks(root)
	if err != nil {
		return err
	}
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return err
		}
		base = filepath.Join(home, ".local", "state")
	}
	hash := sha256.Sum256([]byte(root))
	key := hex.EncodeToString(hash[:])[:20]
	l := launcher{root: root, key: key, directory: filepath.Join(base, "ride-home-router", key), output: output}
	if err = os.MkdirAll(l.directory, 0o700); err != nil {
		return err
	}
	lock, err := os.OpenFile(filepath.Join(l.directory, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = lock.Close() }()
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EINTR) {
			return err
		}
		if err = pause(ctx, 100*time.Millisecond); err != nil {
			return err
		}
	}
	state, found, err := readJSON[environment](l.directory, "state.json")
	if err != nil {
		return err
	}
	if !found {
		if args[0] == "status" || args[0] == "stop" {
			_, err = fmt.Fprintln(output, "No environment for this worktree")
			return err
		}
		runtime := os.Getenv("DEV_RUNTIME")
		if runtime == "" {
			runtime = "podman"
		}
		if _, err = exec.LookPath(runtime); err != nil {
			return errors.New("install Podman or set DEV_RUNTIME to a compatible runtime")
		}
		var owner [16]byte
		var password, encryption [32]byte
		if _, err = rand.Read(owner[:]); err != nil {
			return err
		}
		if _, err = rand.Read(password[:]); err != nil {
			return err
		}
		if _, err = rand.Read(encryption[:]); err != nil {
			return err
		}
		state = environment{Root: root, Owner: hex.EncodeToString(owner[:]), Runtime: runtime, Password: base64.RawURLEncoding.EncodeToString(password[:]), EncryptionKey: base64.StdEncoding.EncodeToString(encryption[:])}
		state.Container = "rhr-dev-" + state.Owner
		if err = l.save("state.json", state); err != nil {
			return err
		}
	}
	if state.Root != root {
		return errors.New("state ownership mismatch")
	}
	switch args[0] {
	case "status":
		return l.status(&state)
	case "stop":
		if err = l.stop(ctx, &state); err != nil {
			return err
		}
		return l.status(&state)
	case "reset":
		if err = l.stop(ctx, &state); err != nil {
			return err
		}
		if _, exists, err := inspect(ctx, &state); err != nil {
			return err
		} else if exists {
			if _, err = container(ctx, &state, "rm", "-v", state.Container); err != nil {
				return err
			}
		}
	}
	if err = l.start(ctx, &state); err != nil {
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 40*time.Second)
		defer cancel()
		return errors.Join(err, l.stop(cleanup, &state))
	}
	return nil
}

func readJSON[T any](directory, name string) (T, bool, error) {
	var value T
	root, err := os.OpenRoot(directory)
	if err != nil {
		return value, false, err
	}
	defer func() { _ = root.Close() }()
	data, err := root.ReadFile(name)
	if errors.Is(err, os.ErrNotExist) {
		return value, false, nil
	}
	if err != nil {
		return value, false, err
	}
	if err = json.Unmarshal(data, &value); err != nil {
		return value, false, err
	}
	return value, true, nil
}

func (l launcher) save(name string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(l.directory, name)
	if err = os.WriteFile(path+".tmp", append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func processStart(pid int) (string, error) {
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	end := strings.LastIndex(string(data), ") ")
	if end < 0 {
		return "", errors.New("invalid process identity")
	}
	fields := strings.Fields(string(data[end+2:]))
	if len(fields) <= 19 {
		return "", errors.New("invalid process identity")
	}
	if fields[0] == "Z" {
		return "", nil
	}
	return fields[19], nil
}

func alive(state *environment) (bool, error) {
	if state.PID <= 0 || state.Start == "" {
		return false, nil
	}
	start, err := processStart(state.PID)
	return start != "" && start == state.Start, err
}

type containerInfo struct {
	Config struct {
		Labels map[string]string `json:"Labels"`
	} `json:"Config"`
	State struct {
		Running bool `json:"Running"`
	} `json:"State"`
}

func container(ctx context.Context, state *environment, args ...string) ([]byte, error) {
	//nolint:gosec // G204: execute the local runtime selected in private worktree state.
	command := exec.CommandContext(ctx, state.Runtime, args...)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	result, err := command.Output()
	if err != nil {
		result = append(result, stderr.Bytes()...)
		return result, fmt.Errorf("%s %s: %w: %s", state.Runtime, args[0], err, strings.TrimSpace(string(result)))
	}
	return result, nil
}

func inspect(ctx context.Context, state *environment) (containerInfo, bool, error) {
	if state.Owner == "" || state.Container == "" || state.Runtime == "" {
		return containerInfo{}, false, errors.New("incomplete container ownership")
	}
	result, err := container(ctx, state, "inspect", state.Container)
	if err != nil {
		message := strings.ToLower(string(result))
		if strings.Contains(message, "no such") || strings.Contains(message, "does not exist") {
			return containerInfo{}, false, nil
		}
		return containerInfo{}, false, err
	}
	var infos []containerInfo
	if err = json.Unmarshal(result, &infos); err != nil {
		return containerInfo{}, false, err
	}
	if len(infos) != 1 || infos[0].Config.Labels["rhr.dev.owner"] != state.Owner {
		return containerInfo{}, false, errors.New("container ownership mismatch; refusing operation")
	}
	return infos[0], true, nil
}

func (l launcher) stop(ctx context.Context, state *environment) error {
	stopCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	running, err := alive(state)
	if err != nil {
		return err
	}
	if running {
		if err = syscall.Kill(state.PID, syscall.SIGTERM); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}
		for {
			running, err = alive(state)
			if err != nil {
				return err
			}
			if !running {
				break
			}
			if err = pause(stopCtx, 100*time.Millisecond); err != nil {
				return errors.New("dev runner did not stop; refusing to remove its database")
			}
		}
	}
	info, found, err := inspect(ctx, state)
	if err != nil {
		return err
	}
	if found && info.State.Running {
		if _, err = container(ctx, state, "stop", state.Container); err != nil {
			return err
		}
	}
	state.PID, state.Start = 0, ""
	if err = l.save("state.json", state); err != nil {
		return err
	}
	return l.removeReadiness()
}

func (l launcher) removeReadiness() error {
	err := os.Remove(filepath.Join(l.directory, "ready.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

func (l launcher) status(state *environment) error {
	running, err := alive(state)
	if err != nil {
		return err
	}
	var ready readiness
	found := false
	if running {
		ready, found, err = readJSON[readiness](l.directory, "ready.json")
		if err != nil {
			return err
		}
	}
	status := "Stopped"
	if found {
		status = "Running\nURL: " + ready.Login + "\nIdentities: " + ready.Login + "&choose=1 (admin / member / denied)"
	}
	_, err = fmt.Fprintf(l.output, "%s\nSynthetic data and travel estimates only.\nState: %s\nLogs: %s\nCleanup: make dev-stop (preserve data); make dev-reset (recreate owned data)\n", status, l.directory, filepath.Join(l.directory, "dev.log"))
	return err
}

func (l launcher) start(ctx context.Context, state *environment) error {
	running, err := alive(state)
	if err != nil {
		return err
	}
	if running {
		_, found, err := readJSON[readiness](l.directory, "ready.json")
		if err != nil {
			return err
		}
		if found {
			return l.status(state)
		}
		return errors.New("runner exists without readiness; run make dev-stop before retrying")
	}
	if err = l.removeReadiness(); err != nil {
		return err
	}
	buildDirectory := filepath.Join(l.directory, "gotmp")
	if err = os.MkdirAll(buildDirectory, 0o700); err != nil {
		return err
	}
	//nolint:gosec // G204: build the fixed dev package into this worktree's private directory.
	build := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(l.directory, "runner.new"), "./tools/dev")
	build.Dir = l.root
	build.Env = append(os.Environ(), "GOTMPDIR="+buildDirectory)
	build.Stdout, build.Stderr = l.output, l.output
	if err = build.Run(); err != nil {
		return fmt.Errorf("build dev runner: %w", err)
	}
	if err = os.Rename(filepath.Join(l.directory, "runner.new"), filepath.Join(l.directory, "runner")); err != nil {
		return err
	}
	info, found, err := inspect(ctx, state)
	if err != nil {
		return err
	}
	if !found {
		_, err = container(ctx, state, "run", "-d", "--name", state.Container, "--label", "rhr.dev.owner="+state.Owner, "-p", "127.0.0.1::5432", "-e", "POSTGRES_PASSWORD="+state.Password, "-e", "POSTGRES_DB=ride_home_router", "docker.io/library/postgres:18")
	} else if !info.State.Running {
		_, err = container(ctx, state, "start", state.Container)
	}
	if err != nil {
		return err
	}
	databaseCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		if _, err = container(databaseCtx, state, "exec", state.Container, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "ride_home_router"); err == nil {
			break
		}
		if err = pause(databaseCtx, 500*time.Millisecond); err != nil {
			return errors.New("owned Postgres did not become ready")
		}
	}
	ports, err := container(ctx, state, "port", state.Container, "5432/tcp")
	if err != nil {
		return err
	}
	address := strings.TrimSpace(string(ports))
	host, port, err := net.SplitHostPort(address)
	if err != nil || host != "127.0.0.1" {
		return errors.New("postgres is not exclusively loopback-bound")
	}
	if number, err := strconv.Atoi(port); err != nil || number < 1 || number > 65535 {
		return errors.New("invalid Postgres port")
	}
	config := struct {
		DatabaseURL   string `json:"database_url"`
		EncryptionKey string `json:"encryption_key"`
		Port          int    `json:"port"`
		Cookie        string `json:"cookie"`
		Capability    string `json:"capability"`
	}{"postgres://postgres:" + state.Password + "@" + address + "/ride_home_router?sslmode=disable", state.EncryptionKey, state.Port, "rhr_dev_" + l.key, state.Owner}
	if err = l.save("config.json", config); err != nil {
		return err
	}
	logFile, err := os.OpenFile(filepath.Join(l.directory, "dev.log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = logFile.Close() }()
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	//nolint:gosec // G204: launch the runner just built in this worktree's private directory.
	proc := exec.CommandContext(context.WithoutCancel(ctx), filepath.Join(l.directory, "runner"), "--state", l.directory)
	proc.Dir = l.root
	proc.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + home}
	proc.Stdout, proc.Stderr = logFile, logFile
	proc.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err = proc.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- proc.Wait() }()
	state.PID = proc.Process.Pid
	state.Start, err = processStart(state.PID)
	if err != nil || state.Start == "" {
		_ = proc.Process.Kill()
		return errors.Join(errors.New("could not identify dev runner"), err)
	}
	if err = l.save("state.json", state); err != nil {
		return err
	}
	readyCtx, readyCancel := context.WithTimeout(ctx, time.Minute)
	defer readyCancel()
	client := &http.Client{Transport: &http.Transport{Proxy: nil}, Timeout: 5 * time.Second}
	defer client.CloseIdleConnections()
	for {
		select {
		case <-done:
			return fmt.Errorf("dev runner exited; see %s", filepath.Join(l.directory, "dev.log"))
		default:
		}
		ready, found, err := readJSON[readiness](l.directory, "ready.json")
		if err != nil {
			return err
		}
		if found {
			request, err := http.NewRequestWithContext(readyCtx, http.MethodGet, ready.URL+"/healthz", nil)
			if err != nil {
				return err
			}
			response, err := client.Do(request)
			if err != nil {
				return err
			}
			if err = response.Body.Close(); err != nil {
				return err
			}
			if response.StatusCode != http.StatusOK {
				return errors.New("application is not ready")
			}
			_, port, err := net.SplitHostPort(strings.TrimPrefix(ready.URL, "http://"))
			if err != nil {
				return err
			}
			state.Port, err = strconv.Atoi(port)
			if err != nil {
				return err
			}
			if err = l.save("state.json", state); err != nil {
				return err
			}
			return l.status(state)
		}
		if err = pause(readyCtx, 500*time.Millisecond); err != nil {
			return fmt.Errorf("dev startup timed out; see %s: %w", filepath.Join(l.directory, "dev.log"), err)
		}
	}
}

func pause(ctx context.Context, duration time.Duration) error {
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
