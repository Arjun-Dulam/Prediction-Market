#pragma once

#include <cstdint>
#include <cstring>
#include <shared_mutex>
#include <string>
#include <unordered_map>

#include "../include/order.hpp"
#include "../include/orderbook.hpp"

class Exchange {
 private:
  std::unordered_map<std::string, std::unique_ptr<OrderBook>> symbol_map;
  mutable std::shared_mutex mutex_;
  std::atomic<uint64_t> next_order_id = 1;

 public:
  Exchange();

  /**
   * @brief If symbol doesn't already exist, method creates new orderbook and
   * add it's unique ptr to global symbol_map
   * @param symbol the symbol to be added
   */
  void add_book(std::string symbol);

  /**
   * @brief Removes the entry for the symbol in symbol_map as well as the
   * corresponding orderbook.
   * @param symbol the symbol to be removed
   */
  void remove_book(std::string symbol);

  /**
   * @brief Appropriately routes incoming order to correct orderbook, doesn't
   * edit orderbook directly
   * @param symbol the symbol corresponding to the order
   * @param order the order to be executed/added to orderbook
   */
  bool add_order(std::string symbol, Order& Order);

  /**
   * @brief Method meant to be called upon by clients, not other methods. This
   * is not as optimized as the function that removes filled orders.
   * @param symbol the symbol for the order to be manually removed
   * @param order_id the unique id for the order to be removed
   */
  bool remove_order(std::string symbol, uint32_t order_id);

  /**
   * @brief Call get_best_bid from the orderbook corresponding to the respective
   * symbol.
   * @return the return value from the orderbook function call
   */

  int32_t get_best_bid(std::string symbol) const;

  int32_t get_best_ask(std::string symbol) const;

  int32_t get_last_trade_price(std::string) const;
};
