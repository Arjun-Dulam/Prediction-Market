// Package engine contains the Go-facing client for the matching-engine gRPC service.
package engine

import (
	"context"
	"fmt"

	enginepb "go-backend/internal/engine/pb"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

// Gateway is the API layer's narrow dependency on the matching engine. Keeping
// this interface small makes HTTP handlers easy to test without a network peer.
type Gateway interface {
	AddOrder(context.Context, string, int32, uint32, enginepb.Side) (uint32, error)
	BestBid(context.Context, string) (int32, error)
	BestAsk(context.Context, string) (int32, error)
	Close() error
}

type Client struct {
	conn   *grpc.ClientConn
	client enginepb.MatchingEngineClient
}

func New(address string) (*Client, error) {
	if address == "" {
		return nil, fmt.Errorf("matching engine address is required")
	}

	conn, err := grpc.NewClient(address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		return nil, fmt.Errorf("connect to matching engine: %w", err)
	}

	return &Client{conn: conn, client: enginepb.NewMatchingEngineClient(conn)}, nil
}

func (c *Client) AddOrder(ctx context.Context, symbol string, price int32, quantity uint32, side enginepb.Side) (uint32, error) {
	response, err := c.client.AddOrder(ctx, &enginepb.OrderSubmission{
		Symbol: symbol,
		Order:  &enginepb.Order{Price: price, Quantity: quantity, Side: side},
	})
	if err != nil {
		return 0, fmt.Errorf("submit order: %w", err)
	}
	return response.GetOrderId(), nil
}

func (c *Client) AddBook(ctx context.Context, symbol string) error {
	_, err := c.client.AddBook(ctx, &enginepb.Symbol{Symbol: symbol})
	if err != nil {
		return fmt.Errorf("add book: %w", err)
	}
	return nil
}

func (c *Client) BestBid(ctx context.Context, symbol string) (int32, error) {
	response, err := c.client.GetBestBid(ctx, &enginepb.Symbol{Symbol: symbol})
	if err != nil {
		return 0, fmt.Errorf("get best bid: %w", err)
	}
	return response.GetPrice(), nil
}

func (c *Client) BestAsk(ctx context.Context, symbol string) (int32, error) {
	response, err := c.client.GetBestAsk(ctx, &enginepb.Symbol{Symbol: symbol})
	if err != nil {
		return 0, fmt.Errorf("get best ask: %w", err)
	}
	return response.GetPrice(), nil
}

func (c *Client) RemoveOrderText(ctx context.Context, symbol string, orderID uint32) (bool, error) {
	response, err := c.client.RemoveOrder(ctx, &enginepb.OrderDeletion{Symbol: symbol, OrderId: orderID})
	if err != nil {
		return false, fmt.Errorf("cancel order: %w", err)
	}
	return response.GetBoolean(), nil
}

func (c *Client) Close() error { return c.conn.Close() }

// Ready performs an actual round trip. NOT_FOUND is the expected response for
// the reserved probe symbol and still proves the engine is reachable.
func (c *Client) Ready(ctx context.Context) error {
	_, err := c.client.GetBestBid(ctx, &enginepb.Symbol{Symbol: "__health_probe__"})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	return err
}
