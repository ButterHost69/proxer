# Proxer

Recycles Proxy, endlessly

A forward proxy that routes user requests through a rotating in-memory pool
of public proxies. Point your browser or curl at it, and every request goes
through the next proxy in the pool: one that answers is recycled, one that
fails is dropped, and when the pool runs dry it is refilled from the public
aggregator.

Features:
- [x] Forward proxy HTTP server (gin): absolute-form requests like `GET http://example.com/index.html HTTP/1.1` are relayed through the pool
- [x] HTTPS tunneling (CONNECT) chained through the pool
- [x] Scrapes publicly available proxies
- [x] Uses multiple go func workers to asynchronously health-check them
- [x] In-memory FIFO queue holds the proxy pool (no Redis, no external state)
- [x] Response aware: a proxy that answers is recycled, one that fails or answers 407/403 is dropped
- [ ] Scores proxies based on latency, availability and response (lower rating => banned)
- [ ] Worker pool so requests themselves are made concurrently, not just the health check

TODO:
- [ ] Find better proxy aggregators
- [ ] Persist the pool (Redis, file, etc.) so it survives a restart
- [ ] Find a good way to update the proxy list without dropping in-flight requests
- [ ] Add extra checks to see if the proxy is doing what it should

## Run

```sh
go run . -addr :8080        # defaults: :8080, 600 workers
```

Every knob lives in `config/config.go` and is a flag:

```sh
./proxer -h

  -addr                  :8080    listen address (proxy.corp.net:8080 is the advertised one)
  -workers               600      concurrent health-check dials during a refresh
  -timeout               10s      budget for one attempt through a proxy
  -max-attempts          5        proxies one request tries before a 502
  -connect-dial-timeout  5s       dial timeout when opening a CONNECT tunnel
  -health-timeout        3s       timeout for one health-check dial
  -scrape-timeout        10s      timeout downloading the aggregator list
  -aggregator            url      proxy list the pool refreshes from
  -gin-mode              release  release (quiet) or debug (route table)
```

Optionally point `proxy.corp.net` at the machine so the advertised address
works locally, then use it:

```sh
echo "127.0.0.1 proxy.corp.net" | sudo tee -a /etc/hosts

curl -x http://proxy.corp.net:8080 http://example.com      # HTTP
curl -x http://proxy.corp.net:8080 https://example.com     # HTTPS (CONNECT)
```

Relative requests are answered locally instead of proxied: `GET /healthz`
reports status and pool size; anything else explains how to use the proxy.

## Test

```sh
go mod tidy                                  # one-time: fetch gin

go test ./test/ -v                            # hermetic: fake proxies over loopback
go test ./... -short                          # skip the live-network test

go test ./test/ -run TestGoogleSearchThroughPool -v   # live: real searches through real proxies
```

The live test is the old `main()` behaviour: warm the pool, run searches
through pooled proxies, and check the responses are real search pages.
