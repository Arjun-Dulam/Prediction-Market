#include <mutex>
#include <shared_mutex>
#include <string>

#include "../include/exchange.hpp"

Exchange::Exchange() {}

void Exchange::add_book(std::string symbol) {
  std::unique_lock<std::shared_mutex> lock(std::mutex);
  if (symbol_map.contains(symbol)) {
    std::unique_lock<std::shared_mutex> unlock(std::mutex);
    return;
  }

  symbol_map.emplace(symbol, std::make_unique<OrderBook>());
  std::unique_lock<std::shared_mutex> unlock(std::mutex);
}

void Exchange::remove_book(std::string symbol) {
  std::unique_lock<std::shared_mutex> lock(std::mutex);
  symbol_map.erase(symbol);
  std::unique_lock<std::shared_mutex> unlock(std::mutex);
  return;
}
