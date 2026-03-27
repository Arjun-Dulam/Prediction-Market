#pragma once
#include <cstddef>
#include <cstdint>
#include <map>
#include <mutex>
#include <thread>
#include <unordered_map>
#include <vector>

#include "order.hpp"
#include "thread_queue.hpp"

class OrderBook {
 private:
  struct OrderLocation {
    Side side;
    int32_t price;
    size_t index;
  };

  std::map<int32_t, std::vector<Order>>
      asks;  // Mapping price to array of corresponding asks
  std::map<int32_t, std::vector<Order>>
      bids;  // Mapping price to array of corresponding bids
  std::unordered_map<uint32_t, OrderLocation>
      order_lookup;  // Mapping order id to order location

  uint64_t orderbook_timestamp = 0;
  uint32_t next_order_id = 0;
  std::vector<Trade> trades;
  size_t deleted_orders_count = 0;
  size_t total_orders_count = 0;
  mutable std::mutex mutex_;

  /**
   * @brief Executes possible trades given the addition of a new order.
   * Modifies executed_trades in place.
   * @param order Order that has recently been added
   * @param executed_trades Pointer to modifiable vector
   */
  void init_trades_with_order(Order& order);

  /**
   * @brief removes filled orders from given map
   * @param map map to remove filled orders from
   */
  void compact_orderbook_helper(std::map<int32_t, std::vector<Order>>& map);

 public:
  OrderBook();
  ~OrderBook();

  ThreadSafeQueue<Order> queue_;
  std::thread worker_;

  /**
   * @brief Adds order to order book, executes and returns possible trades
   * @param order Address of new order to be added to orderbook
   * @return Vector containing trades executed upon new order addition
   */
  void add_order(Order& order);

  /**
   * @brief Scans and removes order if it exists, to be used by traders with no
   * access to Order object.
   * @param order_id order_id
   * @return True if order found and removed, false if order not found
   */
  bool remove_order(uint32_t order_id);

  /**
   * @brief Method used by internal algorithm with access to Order object, skips
   * expensive lookup in order_lookup.
   * @param order order object to be deleted
   * @return True if order found and removed, false if order not found.
   */
  void mark_order_deleted(Order* order);

  /**
   * @brief Shows executed trades
   * @return Vector containing all executed trades
   */
  const std::vector<Trade>& show_trades() const;

  /**
   * @brief Gets the price of the last executed trades
   */
  int32_t get_last_trade_price() const;

  /**
   * @brief Gets the greatest bid price
   */
  int32_t get_best_bid() const;

  /**
   * @brief Gets the lowest asking price
   */
  int32_t get_best_ask() const;

  /**
   * @brief Removes filled/deleted orders from orderbook.
   */
  void compact_orderbook();
};
