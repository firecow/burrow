//go:build integration

package integration

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	burrowImage    = "burrow:integration-test"
	echoImage      = "jmalloc/echo-server:0.3.6"
	networkName    = "burrow-integration"
	proxyPort      = "2184"
	pollInterval   = 100 * time.Millisecond
	stabilizeDelay = 5 * time.Second
)

type containerSpec struct {
	name         string
	image        string
	aliases      []string
	cmd          []string
	exposedPorts []string
	waitStrategy wait.Strategy
}

type stack struct {
	network     testcontainers.Network
	containers  map[string]testcontainers.Container
	retiredLogs []string
}

func TestMain(m *testing.M) {
	buildDir, err := os.MkdirTemp("", "burrow-integration")
	if err != nil {
		log.Fatalf("Failed to create build directory: %v", err)
	}

	goBuild := exec.Command("go", "build", "-o", filepath.Join(buildDir, "burrow"), "./cmd/burrow")
	goBuild.Dir = "../.."
	goBuild.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux")
	goBuild.Stdout = os.Stdout
	goBuild.Stderr = os.Stderr
	if err := goBuild.Run(); err != nil {
		log.Fatalf("Failed to build burrow binary: %v", err)
	}

	dockerBuild := exec.Command("docker", "build", "-t", burrowImage, "-f", "Dockerfile", buildDir)
	dockerBuild.Stdout = os.Stdout
	dockerBuild.Stderr = os.Stderr
	if err := dockerBuild.Run(); err != nil {
		log.Fatalf("Failed to build burrow image: %v", err)
	}

	code := m.Run()
	os.RemoveAll(buildDir)
	os.Exit(code)
}

func TestZeroDowntimeRedeployment(t *testing.T) {
	ctx := context.Background()

	s := &stack{containers: make(map[string]testcontainers.Container)}

	network, err := testcontainers.GenericNetwork(ctx, testcontainers.GenericNetworkRequest{
		NetworkRequest: testcontainers.NetworkRequest{
			Name: networkName,
		},
	})
	require.NoError(t, err)
	s.network = network

	t.Cleanup(func() {
		for name, c := range s.containers {
			if err := c.Terminate(context.Background()); err != nil {
				t.Logf("Failed to terminate %s: %v", name, err)
			}
		}
		if err := s.network.Remove(context.Background()); err != nil {
			t.Logf("Failed to remove network: %v", err)
		}
	})

	t.Cleanup(func() {
		if !t.Failed() {
			return
		}
		for _, retired := range s.retiredLogs {
			t.Log(retired)
		}
		for name, c := range s.containers {
			t.Log(containerLogs(ctx, name, c))
		}
	})

	specs := []containerSpec{
		{
			name:    "echoserver",
			image:   echoImage,
			aliases: []string{"echoserver"},
			waitStrategy: wait.ForHTTP("/").
				WithPort("8080/tcp").
				WithStartupTimeout(30 * time.Second),
		},
		{
			name:    "council",
			image:   burrowImage,
			aliases: []string{"council"},
			cmd:     []string{"council", "--port", "8080"},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		{
			name:    "king1",
			image:   burrowImage,
			aliases: []string{"king1"},
			cmd: []string{
				"king",
				"--council-host=http://council:8080",
				"--host=king1",
				`--tunnel=bind_port=2333 ports=5000-5001`,
				"--location=CPH",
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		{
			name:    "king2",
			image:   burrowImage,
			aliases: []string{"king2"},
			cmd: []string{
				"king",
				"--council-host=http://council:8080",
				"--host=king2",
				`--tunnel=bind_port=2334 ports=5002-5003`,
				"--location=AMS",
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		{
			name:    "ling-alpha",
			image:   burrowImage,
			aliases: []string{"ling-alpha"},
			cmd: []string{
				"ling",
				"--council-host=http://council:8080",
				`--tunnel=name=alpha local_addr=echoserver:8080`,
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(60 * time.Second),
		},
		{
			name:    "ling-beta",
			image:   burrowImage,
			aliases: []string{"ling-beta"},
			cmd: []string{
				"ling",
				"--council-host=http://council:8080",
				`--proxy=name=alpha bind_port=2184`,
			},
			exposedPorts: []string{proxyPort + "/tcp"},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(60 * time.Second),
		},
	}

	for _, spec := range specs {
		c := startContainer(t, ctx, spec)
		s.containers[spec.name] = c
	}

	proxyURL := mappedProxyURL(t, ctx, s.containers["ling-beta"])
	waitForTraffic(t, proxyURL, 60*time.Second)

	t.Run("CouncilRestart", func(t *testing.T) {
		restartSubtest(t, ctx, s, "council", restartStopFirst)
	})

	t.Run("King1Restart", func(t *testing.T) {
		restartSubtest(t, ctx, s, "king1", restartStopFirst)
	})

	t.Run("LingAlphaRestart", func(t *testing.T) {
		restartSubtest(t, ctx, s, "ling-alpha", restartStartFirst)
	})

	t.Run("LingBetaRestart", func(t *testing.T) {
		restartSubtest(t, ctx, s, "ling-beta", restartStartFirst)
	})
}

type restartFunc func(t *testing.T, ctx context.Context, s *stack, name string, target *atomic.Pointer[string])

func restartSubtest(t *testing.T, ctx context.Context, s *stack, name string, restart restartFunc) {
	var errors atomic.Int64
	var total atomic.Int64
	var target atomic.Pointer[string]
	stop := make(chan struct{})
	finished := make(chan struct{})

	proxyURL := mappedProxyURL(t, ctx, s.containers["ling-beta"])
	target.Store(&proxyURL)
	go trafficMonitor(&target, &errors, &total, stop, finished)

	restart(t, ctx, s, name, &target)

	waitForTraffic(t, *target.Load(), 60*time.Second)

	time.Sleep(stabilizeDelay)
	close(stop)
	<-finished

	t.Logf("%s restart: %d/%d requests failed", name, errors.Load(), total.Load())
	require.Zero(
		t,
		errors.Load(),
		"%s restart caused %d errors out of %d requests",
		name,
		errors.Load(),
		total.Load(),
	)
}

func restartStopFirst(
	t *testing.T,
	ctx context.Context,
	s *stack,
	name string,
	_ *atomic.Pointer[string],
) {
	retire(t, ctx, s, name)

	s.containers[name] = startContainer(t, ctx, specForContainer(name))
}

func restartStartFirst(
	t *testing.T,
	ctx context.Context,
	s *stack,
	name string,
	target *atomic.Pointer[string],
) {
	c := startContainer(t, ctx, specForContainer(name))

	if name == "ling-beta" {
		newProxyURL := mappedProxyURL(t, ctx, c)
		waitForTraffic(t, newProxyURL, 60*time.Second)
		target.Store(&newProxyURL)
	}

	retire(t, ctx, s, name)

	s.containers[name] = c
}

func retire(t *testing.T, ctx context.Context, s *stack, name string) {
	old := s.containers[name]

	require.NoError(t, old.Stop(ctx, nil))
	s.retiredLogs = append(s.retiredLogs, containerLogs(ctx, name+" (retired)", old))
	require.NoError(t, old.Terminate(ctx))
	delete(s.containers, name)
}

func containerLogs(ctx context.Context, name string, c testcontainers.Container) string {
	reader, err := c.Logs(ctx)
	if err != nil {
		return fmt.Sprintf("=== %s: failed to read logs: %v", name, err)
	}
	defer reader.Close()

	logs, _ := io.ReadAll(reader)
	return fmt.Sprintf("=== %s\n%s", name, logs)
}

func specForContainer(name string) containerSpec {
	specs := map[string]containerSpec{
		"council": {
			name:    "council",
			image:   burrowImage,
			aliases: []string{"council"},
			cmd:     []string{"council", "--port", "8080"},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		"king1": {
			name:    "king1",
			image:   burrowImage,
			aliases: []string{"king1"},
			cmd: []string{
				"king",
				"--council-host=http://council:8080",
				"--host=king1",
				`--tunnel=bind_port=2333 ports=5000-5001`,
				"--location=CPH",
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		"king2": {
			name:    "king2",
			image:   burrowImage,
			aliases: []string{"king2"},
			cmd: []string{
				"king",
				"--council-host=http://council:8080",
				"--host=king2",
				`--tunnel=bind_port=2334 ports=5002-5003`,
				"--location=AMS",
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(30 * time.Second),
		},
		"ling-alpha": {
			name:    "ling-alpha",
			image:   burrowImage,
			aliases: []string{"ling-alpha"},
			cmd: []string{
				"ling",
				"--council-host=http://council:8080",
				`--tunnel=name=alpha local_addr=echoserver:8080`,
			},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(60 * time.Second),
		},
		"ling-beta": {
			name:    "ling-beta",
			image:   burrowImage,
			aliases: []string{"ling-beta"},
			cmd: []string{
				"ling",
				"--council-host=http://council:8080",
				`--proxy=name=alpha bind_port=2184`,
			},
			exposedPorts: []string{proxyPort + "/tcp"},
			waitStrategy: wait.ForLog("Ready").
				WithStartupTimeout(60 * time.Second),
		},
	}
	return specs[name]
}

func startContainer(
	t *testing.T,
	ctx context.Context,
	spec containerSpec,
) testcontainers.Container {
	t.Helper()

	req := testcontainers.ContainerRequest{
		Image:        spec.image,
		Cmd:          spec.cmd,
		ExposedPorts: spec.exposedPorts,
		Networks:     []string{networkName},
		NetworkAliases: map[string][]string{
			networkName: spec.aliases,
		},
		WaitingFor: spec.waitStrategy,
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	require.NoError(t, err, "Failed to start container %s", spec.name)
	t.Logf("Started container %s", spec.name)
	return c
}

func mappedProxyURL(t *testing.T, ctx context.Context, c testcontainers.Container) string {
	t.Helper()
	host, err := c.Host(ctx)
	require.NoError(t, err)
	port, err := c.MappedPort(ctx, "2184/tcp")
	require.NoError(t, err)
	return fmt.Sprintf("http://%s", net.JoinHostPort(host, port.Port()))
}

func waitForTraffic(t *testing.T, url string, timeout time.Duration) {
	t.Helper()
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := client.Get(url)
		if err == nil {
			io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				t.Logf("Traffic flowing through proxy at %s", url)
				return
			}
		}
		time.Sleep(pollInterval)
	}
	t.Fatalf("Traffic did not start flowing through proxy within %s", timeout)
}

func trafficMonitor(
	target *atomic.Pointer[string],
	errors, total *atomic.Int64,
	stop, finished chan struct{},
) {
	defer close(finished)

	client := &http.Client{Timeout: 3 * time.Second}

	for {
		select {
		case <-stop:
			return
		default:
		}

		total.Add(1)

		resp, err := client.Get(*target.Load())
		if err != nil {
			errors.Add(1)
			log.Printf("traffic error: %v", err)
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errors.Add(1)
				log.Printf("traffic non-OK status: %d", resp.StatusCode)
			}
		}

		time.Sleep(pollInterval)
	}
}
