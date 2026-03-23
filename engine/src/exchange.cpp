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

bool Exchange::add_order(std::string symbol, Order order) {
  std::shared_lock<std::shared_mutex> lock(mutex_);
  auto orderbook = symbol_map.find(symbol);

  if (orderbook == symbol_map.end()) {
    return false;
  }

  orderbook->second->add_order(order);
  return true;
}
