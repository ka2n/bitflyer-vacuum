package main

import (
	"container/ring"
	"context"
	"errors"
	"net/http"
	"net/url"
	"sync"
)

// newProxiedHTTPClient create proxied *http.Client
func newProxiedHTTPClient(proxyURL *url.URL) *http.Client {
	if proxyURL == nil {
		return http.DefaultClient
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)},
	}
}

type ClientPool struct {
	clientRing *ring.Ring
	m          *sync.Mutex
}

func NewClientPool() ClientPool {
	return ClientPool{
		m: new(sync.Mutex),
	}
}

// SetURLs プロキシにURLを設定します
func (p *ClientPool) SetClients(clients []*http.Client) {
	p.m.Lock()
	defer p.m.Unlock()
	p.clientRing = ring.New(len(clients))
	for _, u := range clients {
		p.clientRing.Value = u
		p.clientRing = p.clientRing.Next()
	}
}

// Next 次のプロキシURLを返す
func (p *ClientPool) Next() *http.Client {
	p.m.Lock()
	defer p.m.Unlock()
	if p.clientRing == nil {
		return nil
	}

	client := p.clientRing.Value.(*http.Client)
	p.clientRing = p.clientRing.Next()
	return client
}

func (p *ClientPool) Len() int {
	p.m.Lock()
	defer p.m.Unlock()

	if p.clientRing == nil {
		return 0
	}
	return p.clientRing.Len()
}

// Balancer プロキシ用ラウンドロビン方式のロードバランサ
// Next()を呼ぶと次々にプロキシIPを返します
type Balancer struct {
	urlRing *ring.Ring
	m       *sync.Mutex
}

// NewBalancer 新しいBalancerを返します
func NewBalancer() Balancer {
	return Balancer{
		m: new(sync.Mutex),
	}
}

type ctxkey struct{}

func ContextWithBalancer(ctx context.Context, b *Balancer) context.Context {
	return context.WithValue(ctx, ctxkey{}, b)
}

func BalancerFromContext(ctx context.Context) (*Balancer, error) {
	if b, ok := ctx.Value(ctxkey{}).(*Balancer); ok {
		return b, nil
	}
	return &Balancer{}, errors.New("no Balancer in ctx")
}

// SetURLs プロキシにURLを設定します
func (p *Balancer) SetURLs(urls []*url.URL) {
	p.m.Lock()
	defer p.m.Unlock()
	p.urlRing = ring.New(len(urls))
	for _, u := range urls {
		p.urlRing.Value = u
		p.urlRing = p.urlRing.Next()
	}
}

// Next 次のプロキシURLを返す
func (p *Balancer) Next() *url.URL {
	p.m.Lock()
	defer p.m.Unlock()
	if p.urlRing == nil {
		return nil
	}

	proxyURL := p.urlRing.Value.(*url.URL)
	p.urlRing = p.urlRing.Next()
	return proxyURL
}

func (p *Balancer) Len() int {
	p.m.Lock()
	defer p.m.Unlock()

	if p.urlRing == nil {
		return 0
	}
	return p.urlRing.Len()
}
