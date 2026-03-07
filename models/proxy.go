package models

import "sync"

// Processed
type Proxy struct {
	link  string
	alive bool
	work  bool
	idx   int
	mu    sync.Mutex
}

func (p *Proxy) IsAlive() {
	p.mu.Lock()
	p.alive = true
	p.mu.Unlock()
}

func (p *Proxy) IsDead() {
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
}

func (p *Proxy) IsWorking() {
	p.mu.Lock()
	p.work = true
	p.mu.Unlock()
}

func (p *Proxy) IsDoneWorking() {
	p.mu.Lock()
	p.work = false
	p.mu.Unlock()
}

func (p *Proxy) GetLink() string{
	return p.link
}


type ProxyList struct {
	list []*Proxy
	len  int
	idx  int
	mu   sync.Mutex
}

// Will be only used for initial creation
//
// Treats each proxy as alive
func CreateProxyList(links []string) *ProxyList {
	var pl ProxyList
	pl.list = make([]*Proxy, len(links))
	pl.len = len(links)
	pl.idx = 0

	for i, l := range(links){
		pl.list[i] = &Proxy{
			link: l,
			alive: true,
			work: false,
			idx: i,
		}
	}

	return &pl
}

// Not tested Could Cause Errors
func (pl *ProxyList) GetProxy() *Proxy {
	pl.mu.Lock()
	p := pl.list[pl.idx]

	p.mu.Lock()
	p.idx = pl.idx
	p.mu.Unlock()

	pl.idx = (pl.idx + 1) % pl.len
	pl.mu.Unlock()

	return p
}