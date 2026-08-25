package pondera_test

import (
	"io"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// TestDockerImageServesSPA builds the production image from the repo Dockerfile
// and proves the packaged artifact actually boots and serves. This is the
// verification that the "fatia 6" deliverable (a demoable container for the
// pack.com review) is real and not just a plausible-looking Dockerfile:
// `docker build` must produce an image whose single binary serves the embedded
// SPA shell and the embedded Vue runtime with no network, no asset dir, and no
// build step at run time.
//
// It skips when docker is absent (mirrors the Chrome render test's skip) so
// `go test ./...` stays green on a machine without a container runtime; the
// binary itself imports nothing about docker.
func TestDockerImageServesSPA(t *testing.T) {
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("no docker in PATH; skipping container build/serve check")
	}
	// Serialize nothing external: a unique tag/name keeps parallel repos apart.
	const tag = "pondera-dockertest:ci"
	const name = "pondera-dockertest-ci"

	// Build from the repo root (the test's CWD is the package dir = repo root).
	if out, err := exec.Command("docker", "build", "-t", tag, ".").CombinedOutput(); err != nil {
		t.Fatalf("docker build: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rmi", "-f", tag).Run() })

	// A stale container from a crashed prior run would steal the name.
	_ = exec.Command("docker", "rm", "-f", name).Run()
	// Publish the container's 8080 to an ephemeral loopback port so concurrent
	// runs never collide on a fixed host port.
	if out, err := exec.Command("docker", "run", "-d", "--name", name,
		"-p", "127.0.0.1:0:8080", tag).CombinedOutput(); err != nil {
		t.Fatalf("docker run: %v\n%s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("docker", "rm", "-f", name).Run() })

	portOut, err := exec.Command("docker", "port", name, "8080/tcp").Output()
	if err != nil {
		t.Fatalf("docker port: %v", err)
	}
	// "127.0.0.1:49153\n" (may list several lines; the first loopback mapping is enough).
	hostport := strings.TrimSpace(strings.SplitN(string(portOut), "\n", 2)[0])
	if hostport == "" {
		t.Fatalf("no published port for 8080/tcp: %q", portOut)
	}
	base := "http://" + hostport

	// Poll the root until the containerized server is accepting connections.
	var body string
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := http.Get(base + "/")
		if err == nil && resp.StatusCode == 200 {
			b, _ := io.ReadAll(resp.Body)
			_ = resp.Body.Close()
			body = string(b)
			break
		}
		if err == nil {
			_ = resp.Body.Close()
		}
		if time.Now().After(deadline) {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("GET %s/ never returned 200 (err=%v)\ncontainer logs:\n%s", base, err, logs)
		}
		time.Sleep(300 * time.Millisecond)
	}

	// Load-bearing beyond a bare 200: the served root must be the SPA shell, and
	// the embedded Vue runtime must ship inside the image and be served from the
	// same origin. If the multi-stage build failed to embed the assets, or the
	// wrong file were served, these fail even though the port answers.
	if !strings.Contains(body, `id="app"`) {
		t.Errorf("served root is not the SPA shell (no id=\"app\"):\n%s", firstN(body, 400))
	}
	if !strings.Contains(body, "vue.global.prod.js") {
		t.Errorf("SPA shell does not reference the embedded Vue runtime:\n%s", firstN(body, 400))
	}

	resp, err := http.Get(base + "/vue.global.prod.js")
	if err != nil {
		t.Fatalf("GET %s/vue.global.prod.js: %v", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("GET /vue.global.prod.js = %d, want 200 (Vue runtime not embedded in the image)", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "javascript") {
		t.Errorf("Vue runtime Content-Type = %q, want javascript", ct)
	}
}

func firstN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
