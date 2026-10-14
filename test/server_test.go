// Package test holds the end-to-end exercises for proxer.
//
// server_test.go drives the forward proxy through fake upstream proxies over
// loopback: no internet needed, works everywhere in CI. search_test.go is
// the live-network exercise (real aggregator, real google.com searches) that
// main() used to run at boot; skip it with `go test ./test/ -short`.
package test

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/ButterHost69/proxer/config"
	"github.com/ButterHost69/proxer/models"
	"github.com/ButterHost69/proxer/server"
)

// noRefresh makes the forwarder fail loudly if it ever tries to scrape:
// hermetic tests must only use the proxies they installed themselves.
func noRefresh(*config.Config) (*models.ProxyQueue, error) {
	return models.NewQueue(), errors.New("refresh must not happen in this test")
}

// hostPort reduces a http://host:port URL to the "host:port" the pool wants.
func hostPort(t *testing.T, rawURL string) string {
	t.Helper()

	u, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("bad test URL %q: %v", rawURL, err)
	}

	return u.Host
}

// rawRequest sends a hand-written request over a fresh connection and reads
// the response until the server closes it. All requests include
// "Connection: close" so ReadAll terminates.
func rawRequest(t *testing.T, addr, request string) string {
	t.Helper()

	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatalf("dial %s: %v", addr, err)
	}
	defer conn.Close()

	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatalf("write request: %v", err)
	}

	body, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}

	return string(body)
}

// fakeUpstreamProxy stands in for a real upstream proxy: it expects
// absolute-form request lines (like any HTTP proxy) and echoes what it got.
func fakeUpstreamProxy(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RequestURI, "http://") {
			http.Error(w, "expected absolute-form request, got: "+r.RequestURI, http.StatusBadRequest)

			return
		}

		w.Header().Set("X-Upstream-Proxy", "fake")
		fmt.Fprintf(w, "fetched %s (Host: %s)", r.RequestURI, r.Host)
	}))
	t.Cleanup(server.Close)

	return server
}

// startForwardProxy builds a proxer instance wired to the given fake
// upstreams and served over loopback.
func startForwardProxy(t *testing.T, links ...string) (*httptest.Server, *server.Forwarder) {
	t.Helper()

	pool := models.NewQueueFromLinks(links)
	fwd := server.NewForwarder(pool, config.Default(), noRefresh)

	proxyServer := httptest.NewServer(server.NewHandler(config.Default(), fwd))
	t.Cleanup(proxyServer.Close)

	return proxyServer, fwd
}

func TestForwardsAbsoluteFormRequestThroughPool(t *testing.T) {
	upstream := fakeUpstreamProxy(t)

	proxyServer, _ := startForwardProxy(t, hostPort(t, upstream.URL))

	// Exactly the request shape from a browser configured for
	// proxy.corp.net:8080: absolute URI in the request line.
	resp := rawRequest(t, proxyServer.Listener.Addr().String(),
		"GET http://example.com/index.html HTTP/1.1\r\n"+
			"Host: example.com\r\n"+
			"Connection: close\r\n\r\n")

	if !strings.Contains(resp, "200 OK") {
		t.Fatalf("want 200, got:\n%s", resp)
	}
	if !strings.Contains(resp, "X-Upstream-Proxy: fake") {
		t.Errorf("upstream response headers were not relayed:\n%s", resp)
	}
	if !strings.Contains(resp, "fetched http://example.com/index.html (Host: example.com)") {
		t.Errorf("absolute-form request was not forwarded as-is:\n%s", resp)
	}
}

func TestDeadProxyIsDroppedAndGoodProxyIsRecycled(t *testing.T) {
	upstream := fakeUpstreamProxy(t)

	// Grab a port and let it go: connecting there now fails the same way a
	// dead public proxy does.
	dead, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	deadAddr := dead.Addr().String()
	dead.Close()

	_, fwd := startForwardProxy(t, deadAddr, hostPort(t, upstream.URL))

	resp, used, err := fwd.Do(mustSearch(t, "http://example.com/index.html"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if used.Link() != hostPort(t, upstream.URL) {
		t.Fatalf("request went through %q, want the healthy proxy", used.Link())
	}
	if !used.Alive() {
		t.Error("healthy proxy should be marked alive")
	}

	// The dead proxy must be gone for good, the good one back in rotation.
	if got := fwd.Pool.Len(); got != 1 {
		t.Fatalf("pool held %d proxies after the request, want 1", got)
	}
}

func TestAuthWalledProxyIsRetried(t *testing.T) {
	// The free lists are full of proxies that answer 407 to everything;
	// the request must skip them instead of handing the wall to the client.
	authWall := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusProxyAuthRequired)
	}))
	t.Cleanup(authWall.Close)

	good := fakeUpstreamProxy(t)

	_, fwd := startForwardProxy(t, hostPort(t, authWall.URL), hostPort(t, good.URL))

	resp, used, err := fwd.Do(mustSearch(t, "http://example.com/index.html"))
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()

	if used.Link() != hostPort(t, good.URL) {
		t.Fatalf("request stubbornly used %q instead of skipping the auth wall", used.Link())
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("client got status %d from the auth wall", resp.StatusCode)
	}
	if got := fwd.Pool.Len(); got != 1 {
		t.Errorf("auth-walled proxy should have been dropped, pool holds %d", got)
	}
}

func TestHealthzAndPlainRequestsAreAnsweredLocally(t *testing.T) {
	upstream := fakeUpstreamProxy(t)

	proxyServer, _ := startForwardProxy(t, hostPort(t, upstream.URL))

	resp := rawRequest(t, proxyServer.Listener.Addr().String(),
		"GET /healthz HTTP/1.1\r\nHost: proxy.corp.net\r\nConnection: close\r\n\r\n")
	if !strings.Contains(resp, "200 OK") || !strings.Contains(resp, "pool_ready") {
		t.Errorf("healthz not served:\n%s", resp)
	}

	resp = rawRequest(t, proxyServer.Listener.Addr().String(),
		"GET / HTTP/1.1\r\nHost: proxy.corp.net\r\nConnection: close\r\n\r\n")
	if !strings.Contains(resp, "400") || !strings.Contains(resp, "forward proxy") {
		t.Errorf("plain request should explain proxy usage, got:\n%s", resp)
	}
}

// echoListener accepts connections and echoes every byte back.
func echoListener(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}

			go func(c net.Conn) {
				io.Copy(c, c) //nolint:errcheck // echo until the peer hangs up
				c.Close()
			}(conn)
		}
	}()

	return listener.Addr().String()
}

func TestConnectTunnelsHTTPSThroughThePool(t *testing.T) {
	targetAddr := echoListener(t)

	// Upstream proxy that speaks CONNECT, like real proxies do for HTTPS.
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			http.Error(w, "expected CONNECT", http.StatusBadRequest)

			return
		}

		hijacker, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)

			return
		}

		client, buf, err := hijacker.Hijack()
		if err != nil {
			return
		}

		up, err := net.Dial("tcp", r.Host)
		if err != nil {
			buf.WriteString("HTTP/1.1 502 Bad Gateway\r\n\r\n")
			buf.Flush()
			client.Close()

			return
		}

		buf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n")
		buf.Flush()

		go io.Copy(up, client) //nolint:errcheck // splice until one side hangs up
		io.Copy(client, up)    //nolint:errcheck // splice until one side hangs up
	}))
	t.Cleanup(upstream.Close)

	pool := models.NewQueueFromLinks([]string{hostPort(t, upstream.URL)})
	fwd := server.NewForwarder(pool, config.Default(), noRefresh)

	proxyServer := httptest.NewServer(server.NewHandler(config.Default(), fwd))
	t.Cleanup(proxyServer.Close)

	conn, err := net.Dial("tcp", proxyServer.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if _, err := fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", targetAddr, targetAddr); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}

	status, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT status: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT refused: %s", status)
	}

	// Tunnel is up: bytes now flow to the echo server through both proxies.
	if _, err := conn.Write([]byte("ping through the tunnel\n")); err != nil {
		t.Fatalf("write tunneled bytes: %v", err)
	}

	got := make([]byte, len("ping through the tunnel\n"))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatalf("read tunneled bytes: %v", err)
	}
	if string(got) != "ping through the tunnel\n" {
		t.Fatalf("tunnel corrupted the stream: got %q", got)
	}

	if fwd.Pool.Len() != 1 {
		t.Errorf("good tunnel proxy should be recycled, pool size: %d", fwd.Pool.Len())
	}
}

// mustSearch builds an absolute-form GET, exactly like a client talking to a
// configured proxy.
func mustSearch(t *testing.T, target string) *http.Request {
	t.Helper()

	req, err := http.NewRequest(http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	return req
}
