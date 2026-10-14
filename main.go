// Command proxer runs a forward proxy backed by a rotating in-memory pool of
// public proxies.
//
// Point a client at it (curl -x http://proxy.corp.net:8080 http://example.com
// or a browser whose proxy is proxy.corp.net:8080) and every request is
// relayed through the next proxy in the pool:
//
//	GET http://example.com/index.html HTTP/1.1     (client to proxer)
//	Host: example.com
//
// The pool is an in-memory FIFO queue (see models.ProxyQueue): a proxy that
// answers is pushed back for the next request, one that fails is dropped.
// When the pool drains it is refilled from the aggregator, so the process
// never has to be restarted to recover.
//
// Every tunable lives in the config package (see ./proxer -h); main is just
// wiring.
package main

import (
	"log"

	"github.com/ButterHost69/proxer/config"
	"github.com/ButterHost69/proxer/models"
	"github.com/ButterHost69/proxer/proxies"
	"github.com/ButterHost69/proxer/server"
)

func main() {
	cfg := config.Load()

	log.SetFlags(log.LstdFlags)

	forwarder := server.NewForwarder(models.NewQueue(), cfg, proxies.GetAliveProxies)

	// Warm the pool in the background so the very first client request
	// does not have to wait through a scrape; the server keeps serving and
	// refills on demand if this fails.
	go func() {
		if err := forwarder.Warm(); err != nil {
			log.Printf("initial pool warm-up failed, will retry on demand: %v", err)

			return
		}

		log.Printf("pool warm: %d proxies ready", forwarder.Pool.Len())
	}()

	if err := server.ListenAndServe(cfg, forwarder); err != nil {
		log.Fatal(err)
	}
}
