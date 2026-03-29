# Engine Notes

## ID ownership
- `Exchange` assigns `order_id`.
- `Exchange` assigns timestamps.
- `OrderBook` assigns `trade_id`.
- A trade can be identified by `(symbol, trade_id)`.
