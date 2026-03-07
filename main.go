package main

import (
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/ButterHost69/proxer/proxies"
	"github.com/joho/godotenv"
)

func main() {
	// Connect to Redis
	err := godotenv.Load()
	if err != nil {
		log.Fatal("Error loading .env file")
	}

	var db RedisDB

	err = db.Connect(os.Getenv("REDIS_ADDR"), os.Getenv("REDIS_PASSWORD"))
	if err != nil {
		fmt.Println("Error in Connecting the Redis Server")
		fmt.Println(err)
	}

	proxies, err := proxies.GetAliveProxies(600)
	if err != nil {
		log.Println("could not retrieve proxy list")
		return
	}

	for {
		proxy := proxies.GetProxy()

		proxyURL, _ := url.Parse("http://" + proxy.GetLink())

		client := &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
			},
		}

		rand.Seed(time.Now().UnixNano())
		topics := []string{
			"golang concurrency",
			"p2p networking",
			"distributed systems",
			"minecraft server mods",
		}

		query := topics[rand.Intn(len(topics))]
		searchURL := "http://www.google.com/search?q=" + url.QueryEscape(query)
		req, _ := http.NewRequest("GET", searchURL, nil)
		req.Header.Set("User-Agent", "Mozilla/5.0")

		resp, err := client.Do(req)
		if err != nil {
			fmt.Println("Google request failed:", err)
			continue
		} else {
			fmt.Println("Google response:", resp.Status)
			if resp.StatusCode == 200 {
				body, err := io.ReadAll(resp.Body)
				resp.Body.Close()
				if err != nil {
					panic(err)
				}

				err = os.WriteFile("result.html", body, 0644)
				if err != nil {
					panic(err)
				}

				fmt.Println("Saved response to result.html | len:", len(body))
				return
				
			}
		}

		resp.Body.Close()
	}
}
