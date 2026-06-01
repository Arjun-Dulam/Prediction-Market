package engine

import (
	"context"

	enginepb "go-backend/internal/engine/pb"
)

// AddOrderText adapts the domain service's persisted buy/sell representation
// to the generated protobuf enum without exposing protobufs to that layer.
func (c *Client) AddOrderText(ctx context.Context, symbol string, price int32, quantity uint32, side string) (uint32, error) {
	pbSide := enginepb.Side_SIDE_BUY
	if side == "sell" {
		pbSide = enginepb.Side_SIDE_SELL
	}
	return c.AddOrder(ctx, symbol, price, quantity, pbSide)
}
