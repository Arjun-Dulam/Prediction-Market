#pragma once

#include <optional>
#include <shared_mutex>
#include <string>
#include <unordered_map>

#include "orderbook.hpp"

class Exchange {
 private:
  std::unordered_map<std::string, std::unique_ptr<OrderBook>> symbol_map;
  mutable std::shared_mutex mutex_;

 public:
  Exchange();

  /**
   * @brief If symbol doesn't already exist, method creates new orderbook and
   * add it's unique ptr to global symbol_map
   * @param symbol to be added
   */
  void add_book(std::string symbol);

  /**
   * @brief Removes the entry for the symbol in symbol_map as well as the
   * corresponding orderbook.
   * @param symbol to be removed
   */
  void remove_book(std::string symbol);
