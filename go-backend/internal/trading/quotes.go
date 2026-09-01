package trading

import (
	"context"
	"errors"
)

// readQuotes is called with mu held and only after a successful durable commit.
// Quotes are derived state: read/cache failures never undo an acknowledged trade.
func (s *Service) readQuotes(ctx context.Context, markets []string, supplied []EngineQuote) []MarketQuote {
	bySymbol := make(map[string]EngineQuote)
	for _, q := range supplied {
		bySymbol[q.Symbol] = q
	}
	seen := make(map[string]bool)
	var result []MarketQuote
	for _, id := range markets {
		if seen[id] {
			continue
		}
		seen[id] = true
		symbol := s.markets[id].Symbol
		q, ok := bySymbol[symbol]
		if !ok {
			engine, supported := s.cfg.Engine.(QuoteEngine)
			if !supported {
				continue
			}
			var err error
			q, err = engine.Quote(ctx, symbol)
			if err != nil {
				continue
			}
		}
		result = append(result, MarketQuote{MarketID: id, Symbol: symbol, Bid: q.Bid, Ask: q.Ask, Sequence: s.sequence})
	}
	return result
}
func (s *Service) publishQuotes(quotes []MarketQuote) {
	if len(quotes) > 0 && s.cfg.PublishQuotes != nil {
		s.cfg.PublishQuotes(quotes)
	}
}

// Quote obtains a coherent engine snapshot while no ledger mutation is in flight.
// It rebuilds an expired derived cache with a durable ledger sequence, and refuses
// to expose an uncertain engine state after a failed mutation.
func (s *Service) Quote(ctx context.Context, symbol string) (MarketQuote, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.failure != nil {
		return MarketQuote{}, s.failure
	}
	for id, m := range s.markets {
		if m.Symbol != symbol {
			continue
		}
		if m.Status != Open {
			return MarketQuote{MarketID: id, Symbol: symbol, Bid: -1, Ask: -1, Sequence: s.sequence}, nil
		}
		e, ok := s.cfg.Engine.(QuoteEngine)
		if !ok {
			return MarketQuote{}, errors.New("engine quote snapshot unavailable")
		}
		q, err := e.Quote(ctx, symbol)
		if err != nil {
			return MarketQuote{}, err
		}
		return MarketQuote{MarketID: id, Symbol: symbol, Bid: q.Bid, Ask: q.Ask, Sequence: s.sequence}, nil
	}
	return MarketQuote{}, errors.New("market not found")
}
