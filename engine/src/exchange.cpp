#include <cstdint>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <string>
#include <stdexcept>
#include <unordered_set>

#include "../include/exchange.hpp"

Exchange::Exchange() {}

void Exchange::add_book(std::string symbol) {
  std::unique_lock<std::shared_mutex> lock(mutex_);
  if (symbol_map.contains(symbol)) {
    return;
  }

  symbol_map.emplace(symbol, std::make_unique<OrderBook>());
}

void Exchange::remove_book(std::string symbol) {
  std::unique_lock<std::shared_mutex> lock(mutex_);
  symbol_map.erase(symbol);
  return;
}

uint32_t Exchange::add_order(std::string symbol, Order& order,
                             std::vector<Trade>* executed_trades) {
  order.order_id = next_order_id++;
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return 0;
  }

  orderbook->second->add_order(order, executed_trades);
  return order.order_id;
}

bool Exchange::remove_order(const std::string symbol, const uint32_t order_id) {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return false;
  }

  return orderbook->second->remove_order(order_id);
}

int32_t Exchange::get_best_bid(const std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    throw SYMBOL_NOT_FOUND();
  }
  return orderbook->second->get_best_bid();
}

int32_t Exchange::get_best_ask(const std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    throw SYMBOL_NOT_FOUND();
  }
  return orderbook->second->get_best_ask();
}

int32_t Exchange::get_last_trade_price(const std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    throw SYMBOL_NOT_FOUND();
  }

  return orderbook->second->get_last_trade_price();
}

Exchange::Batch Exchange::add_orders(const std::vector<Submission>& orders) {
  if (orders.empty() || orders.size() > 32) throw std::invalid_argument("batch size must be 1-32");
  std::unique_lock<std::shared_mutex> lock(mutex_);
  for (const auto& input : orders) {
    if (!symbol_map.contains(input.symbol)) throw SYMBOL_NOT_FOUND();
    if (input.quantity == 0 || input.price <= 0 || input.price >= 100 ||
        (input.side != Side::Buy && input.side != Side::Sell))
      throw std::invalid_argument("invalid prediction-market order");
  }
  Batch batch;
  batch.results.reserve(orders.size());
  std::unordered_set<std::string> seen;
  for (const auto& input : orders) {
    Order order(input.price, input.quantity, input.side);
    order.order_id = next_order_id++;
    auto& book = symbol_map.at(input.symbol);
    batch.results.push_back({order.order_id, {}});
    book->add_order(order, &batch.results.back().trades);
    if (seen.insert(input.symbol).second) batch.quotes.push_back({input.symbol, 0, 0});
  }
  for (auto& quote : batch.quotes) {
    const auto& book = symbol_map.at(quote.symbol);
    quote.bid = book->get_best_bid();
    quote.ask = book->get_best_ask();
  }
  return batch;
}

Exchange::Quote Exchange::get_quote(const std::string& symbol) const {
  // A unique lock also excludes add/remove operations holding shared map locks,
  // ensuring bid and ask belong to the same book state.
  std::unique_lock<std::shared_mutex> lock(mutex_);
  auto book = symbol_map.find(symbol);
  if (book == symbol_map.end()) throw SYMBOL_NOT_FOUND();
  return {symbol, book->second->get_best_bid(), book->second->get_best_ask()};
}
