#include <cstdint>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <string>

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

uint32_t Exchange::add_order(std::string symbol, Order& order) {
  order.order_id = next_order_id++;
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return 0;
  }

  orderbook->second->queue_.push(order);
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
