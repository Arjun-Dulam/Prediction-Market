package trading

import "context"

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
