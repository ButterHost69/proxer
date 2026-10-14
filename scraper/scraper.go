// Package scraper downloads candidate proxies from a public aggregator
// and reads them into "ip:port" strings.
package scraper

import (
	"bufio"
	"net/http"
	"strings"

	"github.com/ButterHost69/proxer/config"
)

// GetProxyList downloads cfg.AggregatorURL, one proxy per non-empty line,
// bounded by cfg.ScrapeTimeout.
//
// The stock http.Get applies no timeout at all, so a silent aggregator
// could hang a pool refresh forever; the explicit client here cannot.
func GetProxyList(cfg *config.Config) ([]string, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	cfg = cfg.WithFallbacks()

	client := &http.Client{Timeout: cfg.ScrapeTimeout}

	resp, err := client.Get(cfg.AggregatorURL)
	if err != nil {
		return []string{}, err
	}
	defer resp.Body.Close()

	var links []string
	scanner := bufio.NewScanner(resp.Body)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" {
			links = append(links, line)
		}
	}

	if err := scanner.Err(); err != nil {
		return links, err
	}

	return links, nil
}
