// Package proxies maintains the supply side of the pool: scrape, then
// health-check concurrently, then hand over the survivors as a queue.
package proxies

import (
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ButterHost69/proxer/config"
	"github.com/ButterHost69/proxer/models"
	"github.com/ButterHost69/proxer/scraper"
)

func isProxyAlive(proxy string, timeout time.Duration) bool {
	conn, err := net.DialTimeout("tcp", proxy, timeout)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

// GetAliveProxies scrapes the aggregator, dials every entry with
// cfg.Workers concurrent dials of cfg.HealthCheckTimeout and returns a queue
// holding the survivors.
//
// The returned queue is owned by the caller; on error an empty queue is
// returned alongside the error so callers never have to nil-check it.
func GetAliveProxies(cfg *config.Config) (*models.ProxyQueue, error) {
	if cfg == nil {
		cfg = config.Default()
	}
	cfg = cfg.WithFallbacks()

	if cfg.Workers < 1 {
		return models.NewQueue(), errors.New("workers must be at least 1")
	}

	candidates, err := scraper.GetProxyList(cfg)
	if err != nil {
		log.Println("Error while Getting Proxy List: ", err)
		log.Println("Source: GetAliveProxies()")
		return models.NewQueue(), err
	}

	total := len(candidates)
	log.Println("Total proxies: ", total)

	if total == 0 {
		return models.NewQueue(), errors.New("scraper returned no proxies")
	}

	partitionSize := (total + cfg.Workers - 1) / cfg.Workers // Run Atleast once

	var wg sync.WaitGroup
	start := 0
	limit := total % cfg.Workers

	for i := 0; i < cfg.Workers; i++ {
		// Needed for num_files % num_workers != 0
		if limit != 0 && i == limit {
			partitionSize -= 1
		}

		end := start + partitionSize

		wg.Add(1)

		// inActive Links are set ""
		go func(links []string) {
			for i := range links {
				if !isProxyAlive(links[i], cfg.HealthCheckTimeout) {
					links[i] = ""
				}
			}
			wg.Done()
		}(candidates[start:end])

		start = end
	}
	wg.Wait()

	var alive []string

	for _, p := range candidates {
		if p != "" {
			alive = append(alive, p)
		}
	}

	log.Println("Alive proxies:", len(alive))
	if len(alive) == 0 {
		log.Println("No working proxies")
		return models.NewQueue(), errors.New("no working proxies")
	}

	return models.NewQueueFromLinks(alive), nil
}
