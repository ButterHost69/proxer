package scraper

import (
	"bufio"
	"net/http"
	"strings"
)

// At the very moment we only support
// https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt

// Support for other proxy aggregator's will come in the future

// Returns ip:port
func GetProxyList() ([]string, error) {
	resp, err := http.Get("https://raw.githubusercontent.com/TheSpeedX/SOCKS-List/master/http.txt")
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

	return links, nil
}
