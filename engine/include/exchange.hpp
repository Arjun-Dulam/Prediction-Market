#pragma once

#include <memory>
#include <shared_mutex>
#include <string>
#include <unordered_map>
#include <utility>

#include "orderbook.hpp"

class Exchange {
 private:
  std::unordered_map<std::string, std::unique_ptr<OrderBook>> symbol_map;
  mutable std::shared_mutex mutex_;

 public:
  Exchange();

  void add_book(std::string symbol) {
    if (symbol_map.contains(symbol)) {
      return;
    }

    std::unique_ptr<OrderBook> uniq_ptr = std::make_unique<OrderBook>();
    symbol_map.emplace(symbol, std::move(uniq_ptr));
  }

  



};
