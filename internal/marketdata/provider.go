package marketdata

import (
	"context"
	"math/rand"
	"sync"
	"time"

	"github.com/viviwu/tradeforge/internal/eventbus"
)

type Tick struct {
	Symbol    string
	BidPrice  int64
	BidQty    int64
	AskPrice  int64
	AskQty    int64
	LastPrice int64
	Time      time.Time
}

type Provider struct {
	bus      *eventbus.Bus
	symbols  []string
	interval time.Duration
	mu       sync.Mutex
	prices   map[string]int64
	rng      *rand.Rand
}

func New(bus *eventbus.Bus, symbols []string, interval time.Duration) *Provider {
	p := &Provider{
		bus:      bus,
		symbols:  append([]string(nil), symbols...),
		interval: interval,
		prices:   make(map[string]int64, len(symbols)),
		rng:      rand.New(rand.NewSource(time.Now().UnixNano())),
	}
	for i, sym := range symbols {
		p.prices[sym] = int64(10_000 + i*2_500)
	}
	return p
}

func (p *Provider) Run(ctx context.Context) {
	ticker := time.NewTicker(p.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, sym := range p.symbols {
				p.publishTick(sym)
			}
		}
	}
}

func (p *Provider) publishTick(symbol string) {
	p.mu.Lock()
	last := p.prices[symbol]
	delta := p.rng.Int63n(21) - 10
	next := last + last*delta/1000
	if next < 100 {
		next = 100
	}
	p.prices[symbol] = next
	bidQty := int64(p.rng.Intn(100) + 1)
	askQty := int64(p.rng.Intn(100) + 1)
	p.mu.Unlock()

	tick := Tick{
		Symbol:    symbol,
		BidPrice:  next - 1,
		BidQty:    bidQty,
		AskPrice:  next + 1,
		AskQty:    askQty,
		LastPrice: next,
		Time:      time.Now().UTC(),
	}
	p.bus.Publish(eventbus.Event{
		Type:    eventbus.EventMarketTick,
		Symbol:  symbol,
		Payload: tick,
	})
}

func (p *Provider) LastPrice(symbol string) int64 {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.prices[symbol]
}
