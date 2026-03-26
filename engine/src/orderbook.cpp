#include <iterator>
#include <mutex>

#include "../include/orderbook.hpp"
#define COMPACTION_RATIO 0.15

using std::thread;

OrderBook::~OrderBook() {
  queue_.close();
  worker_.join();
}

OrderBook::OrderBook() {
  next_timestamp = 0;
  next_trade_id = 0;
  next_order_id = 0;
  order_lookup.reserve(15000000);

  worker_ = thread([this] {
    while (true) {
      auto order = queue_.wait_and_pop();
      if (!order) break;
      add_order(*order);
    }
  });
}

void OrderBook::add_order(Order& new_order) {
  new_order.timestamp = next_timestamp++;
  new_order.order_id = next_order_id++;

  init_trades_with_order(new_order);

  // Add order to orderbook if order not completely satisfied

  auto& price_vector = (new_order.side == Side::Buy) ? bids[new_order.price]
                                                     : asks[new_order.price];
  price_vector.push_back(new_order);
  size_t index = size(price_vector) - 1;
  OrderLocation order_location{new_order.side, new_order.price, index};
  order_lookup[new_order.order_id] = order_location;
  total_orders_count++;

  return;
}

void OrderBook::init_trades_with_order(Order& order) {
  while (order.quantity > 0) {
    if (order.side == Side::Buy && asks.empty()) {
      return;
    } else if (order.side == Side::Sell && bids.empty()) {
      return;
    }

    auto intermediate =
        (order.side == Side::Buy) ? asks.begin() : std::prev(bids.end());

    int32_t optimal_existing_price = intermediate->first;
    Order* existing_order = nullptr;
    for (auto& prospect_order : intermediate->second) {
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

    Trade new_trade{
        optimal_existing_price,
        std::min(order.quantity, existing_order->quantity),
        (order.side == Side::Buy) ? order.order_id : existing_order->order_id,
        (order.side == Side::Sell) ? order.order_id : existing_order->order_id};

    next_trade_id++;

    order.quantity -= new_trade.quantity;
    existing_order->quantity -= new_trade.quantity;
    if (existing_order->quantity == 0) {
      mark_order_deleted(existing_order);
    }

    trades.push_back(new_trade);
  }
}

void OrderBook::mark_order_deleted(Order* order) {
  order->deleted_or_filled = true;
  order_lookup.erase(order->order_id);
  deleted_orders_count++;

  if (static_cast<double>(deleted_orders_count) / total_orders_count >
      COMPACTION_RATIO) {
    compact_orderbook();
  }
}

bool OrderBook::remove_order(uint32_t order_id) {
  auto it = order_lookup.find(order_id);
  if (it == order_lookup.end()) {
    return false;
  }

  OrderLocation order_location = it->second;
  auto& target_map = (order_location.side == Side::Buy) ? bids : asks;
  auto& orders_at_price = target_map[order_location.price];
  Order* removing_order = &orders_at_price[order_location.index];

  mark_order_deleted(removing_order);

  return true;
}

const std::vector<Trade>& OrderBook::show_trades() const { return trades; }

int32_t OrderBook::get_last_trade_price() const {
  std::lock_guard<std::mutex> lock(mutex_);
  if (trades.empty()) {
    return -1;
  }
  return trades.back().price;
}

int32_t OrderBook::get_best_ask() const {
  std::lock_guard<std::mutex> lock(mutex_);
  if (asks.empty()) {
    return -1;
  }
  return asks.begin()->first;
};

int32_t OrderBook::get_best_bid() const {
  std::lock_guard<std::mutex> lock(mutex_);
  if (bids.empty()) {
    return -1;
  }
  return std::prev(bids.end())->first;
};

void OrderBook::compact_orderbook() {
  compact_orderbook_helper(bids);
  compact_orderbook_helper(asks);
  total_orders_count -= deleted_orders_count;
  deleted_orders_count = 0;
}

void OrderBook::compact_orderbook_helper(
    std::map<int32_t, std::vector<Order>>& map) {
  for (auto& [price, orders] : map) {
    auto p = [](const Order& order) { return order.deleted_or_filled; };
    orders.erase(std::remove_if(orders.begin(), orders.end(), p), orders.end());

    for (size_t i = 0; i < orders.size(); i++) {
      order_lookup[orders[i].order_id].index = i;
    }
  }

  for (auto it = map.begin(); it != map.end();) {
    if (it->second.empty()) {
      it = map.erase(it);
    } else {
      ++it;
    }
  }
}
