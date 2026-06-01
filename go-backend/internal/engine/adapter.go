package engine

import (
	"context"
	enginepb "go-backend/internal/engine/pb"
	"go-backend/internal/trading"
)

// TradingAdapter exposes string-backed sides to the business layer.
type TradingAdapter struct{ Client *Client }

func (a TradingAdapter) AddBook(ctx context.Context, symbol string) error {
	return a.Client.AddBook(ctx, symbol)
}
func (a TradingAdapter) AddOrder(ctx context.Context, s string, p int32, q uint32, side string) (trading.EngineResult, error) {
	pbSide := enginepb.Side_SIDE_BUY
	if side == "sell" {
		pbSide = enginepb.Side_SIDE_SELL
	}
	response, err := a.Client.client.AddOrder(ctx, &enginepb.OrderSubmission{Symbol: s, Order: &enginepb.Order{Price: p, Quantity: q, Side: pbSide}})
	if err != nil {
		return trading.EngineResult{}, err
	}
	result := trading.EngineResult{OrderID: response.GetOrderId()}
	for _, fill := range response.GetTrades() {
		result.Trades = append(result.Trades, trading.EngineTrade{BuyOrderID: fill.GetBuyOrderId(), SellOrderID: fill.GetSellOrderId(), Price: fill.GetPrice(), Quantity: fill.GetQuantity()})
	}
	return result, nil
}
func (a TradingAdapter) RemoveOrder(ctx context.Context, s string, id uint32) (bool, error) {
	return a.Client.RemoveOrderText(ctx, s, id)
}
