#include "../include/exchange.hpp"

#include <cstdint>
#include <memory>
#include <mutex>
#include <shared_mutex>
#include <string>

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

bool Exchange::add_order(std::string symbol, Order& order) {
  order.order_id = next_order_id++;
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return false;
  }

  orderbook->second->queue_.push(order);
  return true;
}

bool Exchange::remove_order(std::string symbol, uint32_t order_id) {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return false;
  }

  return orderbook->second->remove_order(order_id);
}

int32_t Exchange::get_best_bid(std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);
  return orderbook->second->get_best_bid();
}

int32_t Exchange::get_best_ask(std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);
  return orderbook->second->get_best_ask();
}

int32_t Exchange::get_last_trade_price(std::string symbol) const {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);
  return orderbook->second->get_last_trade_price();
}
