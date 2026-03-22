#include "../include/orderbook.hpp"
#define COMPACTION_RATIO 0.15

OrderBook::OrderBook() {
    next_timestamp = 0;
    next_trade_id = 0;
    next_order_id = 0;
    order_lookup.reserve(15000000);
}

std::vector<Trade> OrderBook::add_order(Order &new_order) {
    std::lock_guard<std::mutex> lock(mutex_);
    new_order.timestamp = next_timestamp++;
    new_order.order_id = next_order_id++;
    std::vector<Trade> executed_trades;

    init_trades_with_order(new_order, &executed_trades);

    if (new_order.quantity == 0) {
        return executed_trades;
    }

    // Add order to orderbook if order not completely satisfied

    auto &price_vector = (new_order.side == Side::Buy) ? bids[new_order.price] : asks[new_order.price];
    price_vector.push_back(new_order);
    size_t index = size(price_vector) - 1;
    OrderLocation order_location{new_order.side, new_order.price, index};
    order_lookup[new_order.order_id] = order_location;
    total_orders_count++;

    return executed_trades;
}

void OrderBook::init_trades_with_order(Order &order, std::vector<Trade> *executed_trades) {
    while (order.quantity > 0) {
        if (order.side == Side::Buy && asks.empty()) {
            return;
        } else if (order.side == Side::Sell && bids.empty()) {
            return;
        }

        auto intermediate = (order.side == Side::Buy) ? asks.begin() : std::prev(bids.end());

        int32_t optimal_existing_price = intermediate->first;
        Order *existing_order = nullptr;
        for (auto &prospect_order : intermediate->second) {
            if (!prospect_order.deleted_or_filled) {
                existing_order = &prospect_order;
                break;
            }
        }

        if (!existing_order) {
            if (order.side == Side::Buy) {
                asks.erase(intermediate);
            } else {
                bids.erase(intermediate);
            }

            continue;
        }

        bool trade_possible;

        if (order.side == Side::Buy) {
            trade_possible = optimal_existing_price <= order.price;
        } else {
            trade_possible = optimal_existing_price >= order.price;
        }

        if (!trade_possible) {
            break;
        }

        Trade new_trade {
            optimal_existing_price,
            std::min(order.quantity, existing_order->quantity),
            (order.side == Side::Buy) ? order.order_id : existing_order->order_id,
            (order.side == Side::Sell) ? order.order_id : existing_order->order_id
        };

        next_trade_id++;

        order.quantity -= new_trade.quantity;
        existing_order->quantity -= new_trade.quantity;
        if (existing_order->quantity == 0) {
            mark_order_deleted(existing_order);
        }

        trades.push_back(new_trade);
        executed_trades->push_back(new_trade);
    }
}

void OrderBook::mark_order_deleted(Order *order) {
    order->deleted_or_filled = true;
    order_lookup.erase(order->order_id);
    deleted_orders_count++;

    if (static_cast<double>(deleted_orders_count) / total_orders_count > COMPACTION_RATIO) {
        compact_orderbook();
    }
}

bool OrderBook::remove_order(uint32_t order_id) {
    std::lock_guard<std::mutex> lock(mutex_);
    auto it = order_lookup.find(order_id);
    if (it == order_lookup.end()) {return false;}

    OrderLocation order_location = it->second;
    auto &target_map = (order_location.side == Side::Buy) ? bids : asks;
    auto &orders_at_price = target_map[order_location.price];
    Order *removing_order = &orders_at_price[order_location.index];

    mark_order_deleted(removing_order);

    return true;
}

const std::vector<Trade>& OrderBook::show_trades() const {
    return trades;
}

void OrderBook::compact_orderbook() {
    compact_orderbook_helper(bids);
    compact_orderbook_helper(asks);
    total_orders_count -= deleted_orders_count;
    deleted_orders_count = 0;
}

void OrderBook::compact_orderbook_helper(std::map<int32_t, std::vector<Order>> &map) {
    for (auto &[price, orders] : map) {
        auto p = [] (const Order &order) {return order.deleted_or_filled;};
        orders.erase(std::remove_if(orders.begin(), orders.end(), p), orders.end());

        for (size_t i = 0; i < orders.size(); i++) {
            order_lookup[orders[i].order_id].index = i;
        }
    }

    for (auto it = map.begin(); it != map.end(); ) {
        if (it->second.empty()) {
            it = map.erase(it);
        } else {
            ++it;
        }
    }
}
