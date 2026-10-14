package server

import (
	"bufio"
	"io"
	"log"
	"net"
	"net/http"
	"strconv"

	"github.com/ButterHost69/proxer/config"
	"github.com/gin-gonic/gin"
)

// New builds the gin engine that serves the forward proxy.
//
// It registers exactly one route, a catch-all for every method, because a
// forward proxy has no paths of its own: the "path" of an incoming request
// is the absolute URL of wherever the client is trying to reach.
func New(cfg *config.Config, fwd *Forwarder) *gin.Engine {
	gin.SetMode(cfg.GinMode)

	router := gin.New()
	router.Use(gin.Logger(), gin.Recovery())

	handler := func(c *gin.Context) {
		if c.Request.Method == http.MethodConnect {
			handleConnect(c, fwd)

			return
		}

		handleForward(c, fwd)
	}

	// The catch-all is the proxy itself: any path is somebody else's URL.
	router.Any("/*path", handler)

	return router
}

// NewHandler wraps New's engine to fix authority-form CONNECT requests
// before routing: gin matches on URL.Path, and a CONNECT request carries no
// path, so it would miss the catch-all route entirely and 404. The rewrite
// has to happen outside gin, since gin's own middleware runs only after
// routing has already happened.
func NewHandler(cfg *config.Config, fwd *Forwarder) http.Handler {
	engine := New(cfg, fwd)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect && (r.URL.Path == "" || r.URL.Path == "/") {
			r.URL.Path = "/connect-tunnel"
		}

		engine.ServeHTTP(w, r)
	})
}

// ListenAndServe starts the proxy on cfg.Addr and blocks until it exits.
func ListenAndServe(cfg *config.Config, fwd *Forwarder) error {
	log.Printf("forward proxy listening on http://%s", cfg.Addr)
	log.Printf("use it like: curl -x http://%s http://example.com", cfg.Addr)

	server := &http.Server{Addr: cfg.Addr, Handler: NewHandler(cfg, fwd)} //nolint:gosec // addr is a local flag, not user input

	return server.ListenAndServe()
}

// handleForward relays an absolute-form request through the pool and copies
// the upstream response back to the client.
func handleForward(c *gin.Context, fwd *Forwarder) {
	// Non-absolute requests are not proxy traffic: either the health probe
	// or someone pointing a browser at the proxy itself.
	if !c.Request.URL.IsAbs() {
		handleLocal(c, fwd)

		return
	}

	resp, proxy, err := fwd.Do(c.Request)
	if err != nil {
		c.String(http.StatusBadGateway, "proxer: %v\n", err)

		return
	}
	defer resp.Body.Close()

	c.Writer.Header().Del("Content-Length") // Go recomputes it while copying
	for name, values := range FilteredHeader(resp.Header) {
		for _, value := range values {
			c.Writer.Header().Add(name, value)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)

	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		// The proxy already did its job; this is the client hanging up
		// mid-download or a broken upstream body.
		log.Printf("streaming body from %s to client failed: %v", proxy.Link(), err)
	}
}

// handleLocal answers the few requests that are not proxy traffic: a
// relative GET /healthz and a helpful error for everything else.
func handleLocal(c *gin.Context, fwd *Forwarder) {
	if c.Request.URL.Path == "/healthz" {
		c.JSON(http.StatusOK, gin.H{
			"status":     "ok",
			"pool_ready": fwd.Pool.Len(),
		})

		return
	}

	c.String(http.StatusBadRequest,
		"proxer is a forward proxy: configure your client to use it and send "+
			"absolute-form requests, e.g.\n\nGET http://example.com/index.html HTTP/1.1\n"+
			"Host: example.com\n")
}

// handleConnect tunnels HTTPS through the pool.
//
// The client asks for "CONNECT example.com:443"; we ask a live proxy in the
// pool for the same thing, then splice raw bytes between the two sockets.
func handleConnect(c *gin.Context, fwd *Forwarder) {
	host := c.Request.Host
	if host == "" {
		host = c.Request.URL.Host
	}
	if host == "" {
		c.String(http.StatusBadRequest, "proxer: CONNECT requires a host\n")

		return
	}

	hijacker, ok := c.Writer.(http.Hijacker)
	if !ok {
		c.String(http.StatusInternalServerError, "proxer: connection cannot be hijacked\n")

		return
	}

	clientConn, clientBuf, err := hijacker.Hijack()
	if err != nil {
		log.Printf("hijack failed: %v", err)

		return
	}
	defer clientConn.Close()

	attempts := fwd.Cfg.MaxAttempts

	var lastErr error

	for attempt := 0; attempt < attempts; attempt++ {
		proxy, ok := fwd.Pool.Pop()
		if !ok {
			if err := fwd.refill(); err != nil {
				writeStatus(clientBuf, http.StatusBadGateway, err)

				return
			}

			proxy, ok = fwd.Pool.Pop()
			if !ok {
				writeStatus(clientBuf, http.StatusBadGateway, nil)

				return
			}
		}

		upstream, err := fwd.dialConnect(proxy, host)
		if err != nil {
			lastErr = err
			proxy.MarkDead()
			log.Printf("proxy %s failed CONNECT to %s: %v", proxy.Link(), host, err)

			continue
		}

		// Tunnelled: tell the client, then become a dumb pipe until either
		// side hangs up.
		proxy.MarkAlive()
		fwd.Pool.Push(proxy)

		if _, err := clientBuf.WriteString("HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
			upstream.Close()

			return
		}
		if err := clientBuf.Flush(); err != nil {
			upstream.Close()

			return
		}

		splice(clientConn, upstream)

		return
	}

	log.Printf("no proxy could CONNECT to %s: %v", host, lastErr)
	writeStatus(clientBuf, http.StatusBadGateway, lastErr)
}

// writeStatus puts a minimal HTTP error response on a hijacked connection.
func writeStatus(buf *bufio.ReadWriter, code int, err error) {
	msg := http.StatusText(code)
	if err != nil {
		msg += ": " + err.Error()
	}

	_, _ = buf.Write([]byte(
		"HTTP/1.1 " + strconv.Itoa(code) + " " + http.StatusText(code) + "\r\n" +
			"Content-Length: " + strconv.Itoa(len(msg)) + "\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"Connection: close\r\n\r\n" + msg))

	_ = buf.Flush()
}

// splice pipes bytes between client and upstream in both directions until
// either side closes.
func splice(client net.Conn, upstream net.Conn) {
	defer upstream.Close()
	defer client.Close()

	done := make(chan struct{})
	go func() {
		io.Copy(upstream, client) //nolint:errcheck // one direction ending closes the other
		close(done)
	}()

	io.Copy(client, upstream) //nolint:errcheck // one direction ending closes the other

	<-done
}
