package matching

import "github.com/viviwu/tradeforge/internal/domain"

type bookOrder struct {
	order     domain.Order
	remaining int64
	arrival   uint64
}

type book struct {
	bids    []*bookOrder
	asks    []*bookOrder
	nextSeq uint64
	index   map[string]*bookOrder
}

func newBook() *book {
	return &book{index: make(map[string]*bookOrder)}
}

func (b *book) add(order domain.Order) {
	b.nextSeq++
	bo := &bookOrder{order: order, remaining: order.Remaining(), arrival: b.nextSeq}
	b.index[order.ID] = bo
	if order.Side == domain.SideBuy {
		idx := searchBid(b.bids, bo)
		b.bids = append(b.bids, nil)
		copy(b.bids[idx+1:], b.bids[idx:])
		b.bids[idx] = bo
		return
	}
	idx := searchAsk(b.asks, bo)
	b.asks = append(b.asks, nil)
	copy(b.asks[idx+1:], b.asks[idx:])
	b.asks[idx] = bo
}

func searchBid(orders []*bookOrder, bo *bookOrder) int {
	lo, hi := 0, len(orders)
	for lo < hi {
		mid := (lo + hi) / 2
		cur := orders[mid]
		if cur.order.Price > bo.order.Price || (cur.order.Price == bo.order.Price && cur.arrival < bo.arrival) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func searchAsk(orders []*bookOrder, bo *bookOrder) int {
	lo, hi := 0, len(orders)
	for lo < hi {
		mid := (lo + hi) / 2
		cur := orders[mid]
		if cur.order.Price < bo.order.Price || (cur.order.Price == bo.order.Price && cur.arrival < bo.arrival) {
			lo = mid + 1
		} else {
			hi = mid
		}
	}
	return lo
}

func (b *book) bestAsk() *bookOrder {
	if len(b.asks) == 0 {
		return nil
	}
	return b.asks[0]
}

func (b *book) bestBid() *bookOrder {
	if len(b.bids) == 0 {
		return nil
	}
	return b.bids[0]
}

func (b *book) remove(bo *bookOrder) {
	if bo == nil {
		return
	}
	if bo.order.Side == domain.SideBuy {
		for i, cur := range b.bids {
			if cur == bo {
				b.bids = append(b.bids[:i], b.bids[i+1:]...)
				break
			}
		}
	} else {
		for i, cur := range b.asks {
			if cur == bo {
				b.asks = append(b.asks[:i], b.asks[i+1:]...)
				break
			}
		}
	}
	delete(b.index, bo.order.ID)
}

func (b *book) cancel(orderID string) *bookOrder {
	bo, ok := b.index[orderID]
	if !ok {
		return nil
	}
	b.remove(bo)
	return bo
}

func (b *book) depth(side domain.Side) int {
	if side == domain.SideBuy {
		return len(b.bids)
	}
	return len(b.asks)
}
