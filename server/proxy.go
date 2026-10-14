// Package server turns the proxy pool into a forward proxy: an HTTP server
// that accepts absolute-form requests (the kind a browser or curl sends to a
// proxy configured as proxy.corp.net:8080) and relays them through the pool.
//
// Request flow per request:
//
//	client  --GET http://example.com/index.html-->  proxer  --same request-->  upstream proxy  -->  example.com
//
// A proxy that answers is pushed back into the pool, one that fails is
// dropped, and when the pool runs dry it is refilled from the aggregator,
// so the server never has to be restarted.
package server

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ButterHost69/proxer/config"
	"github.com/ButterHost69/proxer/models"
)

// RefreshFunc refills the pool from the outside world.
// proxies.GetAliveProxies satisfies this signature.
type RefreshFunc func(cfg *config.Config) (*models.ProxyQueue, error)

// Forwarder relays a client request through the proxy pool.
//
// It is the engine behind the HTTP/HTTPS handlers: the handlers do
// transport-level work (connection hijacking, header hygiene) and the
// forwarder does "pick a proxy, try it, recycle or drop it".
type Forwarder struct {
	// Pool is the proxy pool traffic is routed through. Required.
	Pool *models.ProxyQueue

	// Refresh refills the pool when it runs dry. Required.
	Refresh RefreshFunc

	// Cfg supplies every tunable: timeout, attempts, retry statuses,
	// connect timeout. Required; zero fields fall back to defaults.
	Cfg *config.Config

	// transports caches one *http.Transport per upstream proxy so
	// keep-alive connections to healthy proxies survive between requests.
	transports sync.Map // link -> *http.Transport
}

// NewForwarder returns a forwarder that routes through pool and refills it
// with refresh when needed. Pool and refresh are required; cfg may be nil,
// in which case the defaults are used.
func NewForwarder(pool *models.ProxyQueue, cfg *config.Config, refresh RefreshFunc) *Forwarder {
	if cfg == nil {
		cfg = config.Default()
	}
	cfg = cfg.WithFallbacks()

	if pool == nil {
		pool = models.NewQueue()
	}

	return &Forwarder{
		Pool:    pool,
		Refresh: refresh,
		Cfg:     cfg,
	}
}

// Do sends req through the next proxy in the pool and returns the response.
//
// On success the response body is the caller's to close, and the proxy used
// is pushed back into the pool. On failure the proxy is dropped and the next
// one is tried; if every attempt fails the last error is returned.
func (f *Forwarder) Do(req *http.Request) (*http.Response, *models.Proxy, error) {
	var lastErr error

	for attempt := 0; attempt < f.Cfg.MaxAttempts; attempt++ {
		proxy, ok := f.Pool.Pop()
		if !ok {
			if err := f.refill(); err != nil {
				return nil, nil, fmt.Errorf("no proxies available: %w", err)
			}

			proxy, ok = f.Pool.Pop()
			if !ok {
				return nil, nil, errors.New("no proxies available after refresh")
			}
		}

		resp, err := f.through(proxy, req)
		if err == nil {
			// An auth-walled proxy answers 407/403 to everything; that is
			// the proxy refusing, not the destination replying, so drop it
			// and try the next one instead of showing the client a wall.
			if isProxyRefusal(resp.StatusCode, f.Cfg.RetryStatus) {
				lastErr = fmt.Errorf("%s: %s", proxy.Link(), resp.Status)
				proxy.MarkDead()
				resp.Body.Close()
				log.Printf("proxy %s refused to carry the request: %s", proxy.Link(), resp.Status)

				continue
			}

			proxy.MarkAlive()
			f.Pool.Push(proxy)

			return resp, proxy, nil
		}

		// Dead, refused or too slow: drop it for good.
		lastErr = err
		proxy.MarkDead()
		log.Printf("proxy %s failed: %v", proxy.Link(), err)
	}

	return nil, nil, fmt.Errorf("all %d proxy attempts failed, last error: %w", f.Cfg.MaxAttempts, lastErr)
}

// Warm refills the pool once, ahead of the first request, so clients do
// not wait through a scrape on their first call.
func (f *Forwarder) Warm() error {
	return f.refill()
}

// refill scrapes a fresh batch and merges it into the pool.
func (f *Forwarder) refill() error {
	if f.Refresh == nil {
		return errors.New("no refresh function configured")
	}

	log.Println("proxy pool exhausted, refreshing")

	alive, err := f.Refresh(f.Cfg)
	if err != nil {
		return err
	}

	for _, proxy := range alive.Drain() {
		f.Pool.Push(proxy)
	}

	if f.Pool.Len() == 0 {
		return errors.New("refresh produced no live proxies")
	}

	return nil
}

// through performs a single attempt: one request to one proxy.
func (f *Forwarder) through(proxy *models.Proxy, req *http.Request) (*http.Response, error) {
	// Clone so concurrent requests never share headers/body state, and
	// clear RequestURI because http.Client rejects it (it is a server-side
	// field only).
	outReq := req.Clone(req.Context())
	outReq.RequestURI = ""
	stripHopHeaders(outReq.Header)

	client := &http.Client{
		Timeout:   f.Cfg.Timeout,
		Transport: f.transportFor(proxy.Link()),
	}

	return client.Do(outReq)
}

// transportFor returns a cached Transport pinned to the upstream proxy, or
// builds and caches one on first use. Caching keeps keep-alive connections
// to a healthy proxy alive between client requests.
func (f *Forwarder) transportFor(link string) *http.Transport {
	if cached, ok := f.transports.Load(link); ok {
		return cached.(*http.Transport)
	}

	proxyURL, err := url.Parse("http://" + link)
	if err != nil {
		// A malformed proxy must not panic the request path; a transport
		// with a nil Proxy never routes, so the attempt fails and the
		// proxy is dropped below.
		proxyURL = nil
	}

	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL)}
	actual, _ := f.transports.LoadOrStore(link, transport)

	return actual.(*http.Transport)
}

// dialConnect dials the upstream proxy and negotiates a CONNECT tunnel to
// host ("host:port"). The returned connection streams raw bytes between
// this process and host through the proxy.
func (f *Forwarder) dialConnect(proxy *models.Proxy, host string) (net.Conn, error) {
	conn, err := net.DialTimeout("tcp", proxy.Link(), f.Cfg.ConnectDialTimeout)
	if err != nil {
		return nil, err
	}

	if err := conn.SetDeadline(time.Now().Add(f.Cfg.Timeout)); err != nil {
		conn.Close()
		return nil, err
	}

	connectReq := "CONNECT " + host + " HTTP/1.1\r\nHost: " + host + "\r\n\r\n"
	if _, err := io.WriteString(conn, connectReq); err != nil {
		conn.Close()
		return nil, err
	}

	// The upstream proxy answers a small status block, then the socket
	// becomes an opaque pipe. The whole block must be consumed, or a proxy
	// that sends headers would have them read back as tunnel payload.
	reply, err := readConnectReply(conn)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("bad CONNECT response from %s: %w", proxy.Link(), err)
	}

	// Tunnelled: hand the socket back with no deadline so the goroutines
	// that splice it do not get cut off mid-copy.
	if err := conn.SetDeadline(time.Time{}); err != nil {
		conn.Close()
		return nil, err
	}

	if !strings.Contains(reply, " 200 ") {
		conn.Close()
		return nil, fmt.Errorf("%s refused CONNECT to %s: %s", proxy.Link(), host, firstLine(reply))
	}

	return conn, nil
}

// readConnectReply reads a full HTTP response block (up to the blank line)
// from conn. A proxy that rambles past 8 KiB is a proxy we drop.
func readConnectReply(conn net.Conn) (string, error) {
	var buf []byte
	single := make([]byte, 1)

	for {
		n, err := conn.Read(single)
		if err != nil {
			return "", err
		}
		if n == 0 {
			continue
		}

		buf = append(buf, single[0])

		if bytes.HasSuffix(buf, []byte("\r\n\r\n")) {
			break
		}

		if len(buf) > 8192 {
			return "", errors.New("response block too long")
		}
	}

	return string(buf), nil
}

// firstLine returns the first line of a response block, for error messages.
func firstLine(block string) string {
	if idx := strings.Index(block, "\r\n"); idx >= 0 {
		return block[:idx]
	}

	return block
}

// isProxyRefusal reports whether code is one of the listed proxy-level
// refusals. An empty list means "never refuse".
func isProxyRefusal(code int, refusals []int) bool {
	for _, refusal := range refusals {
		if code == refusal {
			return true
		}
	}

	return false
}

// stripHopHeaders removes per-connection headers so they are not leaked to
// the upstream proxy (or back to the client).
func stripHopHeaders(header http.Header) {
	// Headers named in Connection are hop-by-hop too, so capture the
	// names before the loop below deletes the header itself.
	connectionNamed := header.Values("Connection")

	for _, name := range hopByHopHeaders {
		header.Del(name)
	}

	for _, listed := range connectionNamed {
		for _, extra := range strings.Split(listed, ",") {
			header.Del(strings.TrimSpace(extra))
		}
	}
}

// hopByHopHeaders are headers that only apply to a single transport-level
// connection and must not be forwarded in either direction (RFC 9110 §7.6.1).
var hopByHopHeaders = []string{
	"Connection",
	"Proxy-Connection",
	"Keep-Alive",
	"Proxy-Authenticate",
	"Proxy-Authorization",
	"TE",
	"Trailer",
	"Transfer-Encoding",
	"Upgrade",
}

// FilteredHeader copies header for relaying to the client, minus
// hop-by-hop headers, which no proxy may forward.
func FilteredHeader(header http.Header) http.Header {
	out := make(http.Header, len(header))
	for name, values := range header {
		skip := false
		for _, hop := range hopByHopHeaders {
			if http.CanonicalHeaderKey(hop) == http.CanonicalHeaderKey(name) {
				skip = true
				break
			}
		}
		if skip {
			continue
		}

		for _, value := range values {
			out.Add(name, value)
		}
	}

	return out
}
