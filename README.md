# Proxer

Recycles Proxy, endlessly

Features:
- [ ] Scrapes Publicly available proxies
- [ ] Uses multiple go func workers to aysnchronously make requests
- [ ] Response aware, scores proxies based on latency and availibility and response (lowers proxy rating is banned)
- [ ] Uses redis for submit req/resp



TODO:
- [ ] Find Better Proxy Aggreagators
- [ ] Add the proxy pipeline
  - [ ] Read and Write to Redis
  - [ ] Deploy Workers
- [ ] Find a good way to update the proxy list without going down
- [ ] Add extra checks to see if the proxy is doing what it should