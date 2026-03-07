package proxies

import (
	"errors"
	"log"
	"net"
	"sync"
	"time"

	"github.com/ButterHost69/proxer/models"
	"github.com/ButterHost69/proxer/scraper"
)

func isProxyAlive(proxy string) bool {
	conn, err := net.DialTimeout("tcp", proxy, 3*time.Second)
	if err != nil {
		return false
	}
	conn.Close()
	return true
}

func GetAliveProxies(numWorkers int) (*models.ProxyList, error) {

	unchecked_proxies, err := scraper.GetProxyList()
	if err != nil {
		log.Println("Error while Getting Proxy List: ", err)
		log.Println("Source: getAliveProxies()")
		return &models.ProxyList{}, err
	}

	n_proxies := len(unchecked_proxies)
	log.Println("Total proxies: ", n_proxies)

	partitionSize := (n_proxies + numWorkers - 1) / numWorkers // Run Atleast once

	var wg sync.WaitGroup
	start := 0
	limit := (n_proxies) % numWorkers

	for i := 0; i < numWorkers; i++ {
		// Needed for num_files % num_workers != 0
		if limit != 0 && i == limit {
			partitionSize -= 1
		}

		end := start + partitionSize

		wg.Add(1)

		// inActive Links are set ""
		go func(links []string) {
			for i := range links {
				if !isProxyAlive(links[i]) {
					links[i] = ""
				}
			}
			wg.Done()
		}(unchecked_proxies[start:end])

		start = end
	}
	wg.Wait()

	var alive []string

	for _, p := range unchecked_proxies {
		if p != "" {
			alive = append(alive, p)
		}
	}

	log.Println("Alive proxies:", len(alive))
	if len(alive) == 0 {
		log.Println("No working proxies")
		return &models.ProxyList{}, errors.New("no working proxies")
	}

	return models.CreateProxyList(alive), nil
}
