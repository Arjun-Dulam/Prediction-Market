package engine

import (
	"context"
	"fmt"
	enginepb "go-backend/internal/engine/pb"
	"go-backend/internal/trading"
)

func (a TradingAdapter) AddOrders(ctx context.Context, orders []trading.EngineOrder) (trading.EngineBatch, error) {
	if len(orders) == 0 || len(orders) > 32 {
		return trading.EngineBatch{}, fmt.Errorf("batch size must be 1-32")
	}
	request := &enginepb.OrderBatch{}
	for _, o := range orders {
		side := enginepb.Side_SIDE_BUY
		if o.Side == "sell" {
			side = enginepb.Side_SIDE_SELL
		} else if o.Side != "buy" {
			return trading.EngineBatch{}, fmt.Errorf("invalid engine side")
		}
		request.Orders = append(request.Orders, &enginepb.OrderSubmission{Symbol: o.Symbol, Order: &enginepb.Order{Price: o.Price, Quantity: o.Quantity, Side: side}})
	}
	response, err := a.Client.client.AddOrders(ctx, request)
	if err != nil {
		return trading.EngineBatch{}, err
	}
	if len(response.Results) != len(orders) {
		return trading.EngineBatch{}, fmt.Errorf("invalid engine batch result count")
	}
	batch := trading.EngineBatch{}
	for _, r := range response.Results {
		if r.GetOrderId() == 0 {
			return trading.EngineBatch{}, fmt.Errorf("invalid engine order ID")
		}
		result := trading.EngineResult{OrderID: r.GetOrderId()}
		for _, f := range r.GetTrades() {
			result.Trades = append(result.Trades, trading.EngineTrade{BuyOrderID: f.GetBuyOrderId(), SellOrderID: f.GetSellOrderId(), Price: f.GetPrice(), Quantity: f.GetQuantity()})
		}
		batch.Results = append(batch.Results, result)
	}
	for _, q := range response.Quotes {
		batch.Quotes = append(batch.Quotes, trading.EngineQuote{Symbol: q.GetSymbol(), Bid: q.GetBid(), Ask: q.GetAsk()})
	}
	return batch, nil
}
func (a TradingAdapter) Quote(ctx context.Context, symbol string) (trading.EngineQuote, error) {
	q, err := a.Client.client.GetQuote(ctx, &enginepb.Symbol{Symbol: symbol})
	if err != nil {
		return trading.EngineQuote{}, err
	}
	return trading.EngineQuote{Symbol: q.GetSymbol(), Bid: q.GetBid(), Ask: q.GetAsk()}, nil
}
