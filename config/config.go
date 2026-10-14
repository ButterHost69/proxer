// Package config is the single source of truth for every tunable in proxer.
//
// Everything the runtime can adjust lives in Config, in one place:
//
//	./proxer -h                      # every knob is a flag
//	./proxer -addr proxy.corp.net:8080 -workers 900 -timeout 15s
//
// Default() supplies the values used by tests and by anything that
// constructs a Config by hand; Load() layers flags on top for the binary.
// withFallbacks repairs partially-filled Configs so a zero value never
// means "no timeout" or "no attempts".
package config

import (
	"flag"
	"net/http"
	"time"
)

// Defaults for every knob. They are duplicated here rather than referenced
// from the packages that consume them so config has no dependencies and
// every default can be read at a glance.
const (
	// DefaultAddr is where the proxy listens.
	DefaultAddr = ":9666"

	// DefaultWorkers is the fan-out of the concurrent health check.
	DefaultWorkers = 600

	// DefaultTimeout bounds one attempt at a request through a proxy.
	DefaultTimeout = 10 * time.Second

	// DefaultMaxAttempts bounds how many proxies one client request tries
	// before the client gets a 502.
	DefaultMaxAttempts = 5

	// DefaultConnectDialTimeout bounds the TCP dial to an upstream proxy
	// when opening a CONNECT tunnel.
	DefaultConnectDialTimeout = 5 * time.Second

	// DefaultHealthCheckTimeout bounds a single TCP dial of the pool
	// health check.
	DefaultHealthCheckTimeout = 3 * time.Second

	// DefaultScrapeTimeout bounds downloading the aggregator list. The
	// stock http.Get has no timeout at all, so a silent aggregator could
	// hang a refill forever.
	DefaultScrapeTimeout = 10 * time.Second

	// DefaultAggregatorURL is the public list proxer scrapes.
	DefaultAggregatorURL = "https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt"

	// DefaultGinMode is gin's own mode string (debug/release/test).
	DefaultGinMode = "release"
)

// Config holds every runtime tunable. The three groups are roughly
//
//	Server    - how the process listens and logs
//	Forwarder - how requests are carried through the pool
//	Supply    - where replacement proxies come from and how hard we poke them
type Config struct {
	// Addr is the listen address, host:port.
	Addr string

	// GinMode is gin's mode: release keeps the router quiet, debug prints
	// the route table at startup.
	GinMode string

	// Workers is the concurrent-dial fan-out used when the pool is
	// refreshed.
	Workers int

	// Timeout bounds one attempt at carrying a request through a proxy,
	// including reading the response.
	Timeout time.Duration

	// MaxAttempts is how many different proxies a single client request
	// tries before giving up with a 502.
	MaxAttempts int

	// ConnectDialTimeout bounds the TCP dial to an upstream proxy made
	// while opening a CONNECT tunnel.
	ConnectDialTimeout time.Duration

	// RetryStatus is the set of upstream status codes treated as "the
	// proxy refused the request" rather than "the destination answered".
	// Auth-walled public proxies answer 407/403 to absolutely everything;
	// without this the wall would be relayed to the client.
	RetryStatus []int

	// AggregatorURL is the proxy list that refreshes the pool.
	AggregatorURL string

	// ScrapeTimeout bounds each download of AggregatorURL.
	ScrapeTimeout time.Duration

	// HealthCheckTimeout bounds a single TCP dial of the pool health
	// check.
	HealthCheckTimeout time.Duration
}

// Default returns a Config with every knob at its default value.
func Default() *Config {
	return &Config{
		Addr:               DefaultAddr,
		GinMode:            DefaultGinMode,
		Workers:            DefaultWorkers,
		Timeout:            DefaultTimeout,
		MaxAttempts:        DefaultMaxAttempts,
		ConnectDialTimeout: DefaultConnectDialTimeout,
		RetryStatus:        []int{http.StatusProxyAuthRequired, http.StatusForbidden},
		AggregatorURL:      DefaultAggregatorURL,
		ScrapeTimeout:      DefaultScrapeTimeout,
		HealthCheckTimeout: DefaultHealthCheckTimeout,
	}
}

// Load parses flags over the defaults and returns the resulting Config.
// It calls flag.Parse, so it must run before flags are read any other way.
func Load() *Config {
	cfg := Default()

	flag.StringVar(&cfg.Addr, "addr", cfg.Addr,
		"address to listen on (proxy.corp.net:8080 is the advertised one)")
	flag.StringVar(&cfg.GinMode, "gin-mode", cfg.GinMode,
		"gin mode: release (quiet) or debug (prints the route table)")
	flag.IntVar(&cfg.Workers, "workers", cfg.Workers,
		"concurrent workers used when health-checking and refreshing the pool")
	flag.DurationVar(&cfg.Timeout, "timeout", cfg.Timeout,
		"timeout for one request attempt through a proxy")
	flag.IntVar(&cfg.MaxAttempts, "max-attempts", cfg.MaxAttempts,
		"proxies a single client request tries before a 502")
	flag.DurationVar(&cfg.ConnectDialTimeout, "connect-dial-timeout", cfg.ConnectDialTimeout,
		"timeout dialing an upstream proxy to open a CONNECT tunnel")
	flag.StringVar(&cfg.AggregatorURL, "aggregator", cfg.AggregatorURL,
		"proxy list URL the pool is refreshed from")
	flag.DurationVar(&cfg.ScrapeTimeout, "scrape-timeout", cfg.ScrapeTimeout,
		"timeout for downloading the aggregator list")
	flag.DurationVar(&cfg.HealthCheckTimeout, "health-timeout", cfg.HealthCheckTimeout,
		"timeout for a single TCP dial of a candidate proxy")

	flag.Parse()

	return cfg.WithFallbacks()
}

// WithFallbacks repairs zero values so a hand-built Config (tests, future
// embedders) behaves sanely: zero timeout means infinite, which is never
// what anyone wants here. A nil receiver yields the defaults.
func (c *Config) WithFallbacks() *Config {
	if c == nil {
		return Default()
	}

	fixed := *c

	if fixed.Addr == "" {
		fixed.Addr = DefaultAddr
	}
	if fixed.GinMode == "" {
		fixed.GinMode = DefaultGinMode
	}
	if fixed.Workers < 1 {
		fixed.Workers = DefaultWorkers
	}
	if fixed.Timeout <= 0 {
		fixed.Timeout = DefaultTimeout
	}
	if fixed.MaxAttempts < 1 {
		fixed.MaxAttempts = DefaultMaxAttempts
	}
	if fixed.ConnectDialTimeout <= 0 {
		fixed.ConnectDialTimeout = DefaultConnectDialTimeout
	}
	if fixed.AggregatorURL == "" {
		fixed.AggregatorURL = DefaultAggregatorURL
	}
	if fixed.ScrapeTimeout <= 0 {
		fixed.ScrapeTimeout = DefaultScrapeTimeout
	}
	if fixed.HealthCheckTimeout <= 0 {
		fixed.HealthCheckTimeout = DefaultHealthCheckTimeout
	}
	if len(fixed.RetryStatus) == 0 {
		fixed.RetryStatus = []int{http.StatusProxyAuthRequired, http.StatusForbidden}
	}

	return &fixed
}
