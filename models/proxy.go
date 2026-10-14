// Package models holds the in-memory representation of the proxy pool.
//
// Nothing in here touches the network, the disk or Redis: the pool lives and
// dies with the process.
package models

import (
	"container/list"
	"sync"
)

// Proxy is a single "ip:port" endpoint that requests can be routed through.
//
// The alive/work flags record what the proxy did on its last request so the
// pool can react to it. They are informational for now — scheduling is still
// plain FIFO.
type Proxy struct {
	link  string
	alive bool
	work  bool
	mu    sync.Mutex
}

// NewProxy returns a proxy for link, assumed alive until proven otherwise.
func NewProxy(link string) *Proxy {
	return &Proxy{link: link, alive: true}
}

// MarkAlive records that the proxy answered its last request.
func (p *Proxy) MarkAlive() {
	p.mu.Lock()
	p.alive = true
	p.mu.Unlock()
}

// MarkDead records that the proxy failed its last request.
func (p *Proxy) MarkDead() {
	p.mu.Lock()
	p.alive = false
	p.mu.Unlock()
}

// MarkInUse records that a worker currently holds this proxy.
func (p *Proxy) MarkInUse() {
	p.mu.Lock()
	p.work = true
	p.mu.Unlock()
}

// MarkIdle records that no worker holds this proxy any more.
func (p *Proxy) MarkIdle() {
	p.mu.Lock()
	p.work = false
	p.mu.Unlock()
}

// Alive reports whether the proxy answered its last request.
func (p *Proxy) Alive() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.alive
}

// InUse reports whether a worker currently holds this proxy.
func (p *Proxy) InUse() bool {
	p.mu.Lock()
	defer p.mu.Unlock()

	return p.work
}

// Link returns the "ip:port" the proxy listens on.
func (p *Proxy) Link() string {
	return p.link
}

// ProxyQueue is a thread-safe FIFO queue of proxies.
//
// Workers Pop a proxy when they need one and Push it back when they are done
// with it, which is what makes the pool "endless": a proxy that keeps working
// stays in rotation, one that fails is simply never pushed back.
type ProxyQueue struct {
	mu    sync.Mutex
	items *list.List
}

// NewQueue returns an empty queue, ready for use.
func NewQueue() *ProxyQueue {
	return &ProxyQueue{items: list.New()}
}

// NewQueueFromLinks builds a queue pre-loaded with one proxy per link, each
// assumed alive. Blank links are skipped.
func NewQueueFromLinks(links []string) *ProxyQueue {
	queue := NewQueue()

	for _, link := range links {
		if link == "" {
			continue
		}
		queue.items.PushBack(NewProxy(link))
	}

	return queue
}

// Push adds p to the back of the queue. Nil proxies are ignored.
func (q *ProxyQueue) Push(p *Proxy) {
	if p == nil {
		return
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	q.items.PushBack(p)
}

// Pop removes and returns the proxy at the front of the queue. The second
// return value is false when the queue is empty, so callers can tell
// "nothing to do" apart from a real proxy.
func (q *ProxyQueue) Pop() (*Proxy, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	front := q.items.Front()
	if front == nil {
		return nil, false
	}

	q.items.Remove(front)

	proxy, _ := front.Value.(*Proxy)
	return proxy, true
}

// Drain removes and returns everything currently queued, leaving the queue
// empty. It exists so a freshly scraped pool can be merged into a live one
// without exposing the internals of either queue.
func (q *ProxyQueue) Drain() []*Proxy {
	q.mu.Lock()
	defer q.mu.Unlock()

	drained := make([]*Proxy, 0, q.items.Len())
	for front := q.items.Front(); front != nil; front = q.items.Front() {
		q.items.Remove(front)
		if proxy, ok := front.Value.(*Proxy); ok {
			drained = append(drained, proxy)
		}
	}

	return drained
}

// Len reports how many proxies are currently waiting in the queue.
func (q *ProxyQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()

	return q.items.Len()
}
