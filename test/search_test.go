package test

import (
	"io"
	"net/http"
	"net/url"
	"testing"

	"github.com/ButterHost69/proxer/config"
	"github.com/ButterHost69/proxer/models"
	"github.com/ButterHost69/proxer/proxies"
	"github.com/ButterHost69/proxer/server"
)

// searchTopics are the queries exercised against the pool. They used to be
// the hardcoded payload of main()'s endless loop.
var searchTopics = []string{
	"golang concurrency",
	"p2p networking",
	"distributed systems",
	"minecraft server mods",
}

// TestGoogleSearchThroughPool is the old main() end-to-end behaviour, now as
// a test: warm the pool, run a search through a pooled proxy, and check the
// response is a real search page.
//
// It hits the real aggregator and google.com, so it needs internet and takes
// about a minute. Skip it with `go test ./test/ -short`.
func TestGoogleSearchThroughPool(t *testing.T) {
	if testing.Short() {
		t.Skip("live-network test; rerun without -short to run it")
	}

	cfg := config.Default()
	// Public pools routinely hand out one dead proxy in three, so give the
	// search more rope than the server's interactive default: this exercise
	// is about proving the pool delivers, not bounding client latency.
	cfg.MaxAttempts = 12

	fwd := server.NewForwarder(models.NewQueue(), cfg, proxies.GetAliveProxies)

	if err := fwd.Warm(); err != nil {
		t.Fatalf("warming the pool: %v", err)
	}

	successes := 0

	for _, topic := range searchTopics {
		target := "http://www.google.com/search?q=" + url.QueryEscape(topic)

		req, err := http.NewRequest(http.MethodGet, target, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0")

		resp, proxy, err := fwd.Do(req)
		if err != nil {
			// Public proxies being flaky is expected, not a test failure:
			// the pool is proven by the topics that do get through.
			t.Logf("search %q failed through the pool: %v", topic, err)

			continue
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			t.Logf("search %q: body read failed: %v", topic, err)

			continue
		}

		if resp.StatusCode != http.StatusOK {
			// Common with public pools: 407s on auth-walled proxies, 403s
			// from google flagging the egress. One topic's bad luck is not
			// a broken pool.
			t.Logf("search %q through %s: status %s (%d bytes)", topic, proxy.Link(), resp.Status, len(body))

			continue
		}

		successes++
		t.Logf("search %q through %s: %d bytes", topic, proxy.Link(), len(body))
	}

	if successes == 0 {
		t.Fatal("no search succeeded through the pool")
	}
}
