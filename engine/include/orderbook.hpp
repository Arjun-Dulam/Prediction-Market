#pragma once
#include <map>
#include <vector>
#include <unordered_map>
#include <cstdint>
#include <cstddef>
#include <mutex>

#include "order.hpp"

class OrderBook {
    // TODO: Critical Design Flaw: Trader has no way of accesing order_id after creating order. Create Trader struct and
    // add order-id to some optimal DS that trader has access to.
private:
    struct OrderLocation {
        Side side;
        int32_t price;
        size_t index;
    };

    std::map<int32_t, std::vector<Order>> asks; // Mapping price to array of corresponding asks
    std::map<int32_t, std::vector<Order>> bids; // Mapping price to array of corresponding bids
    std::unordered_map<uint32_t, OrderLocation> order_lookup; // Mapping order id to order location
    std::vector<Trade> trades;
    uint64_t next_timestamp;
    uint32_t next_trade_id;
    uint32_t next_order_id;
    size_t deleted_orders_count = 0;
    size_t total_orders_count = 0;
    std::mutex mutex_;

    /**
     * @brief Executes possible trades given the addition of a new order. Modifies executed_trades in place.
     * @param order Order that has recently been added
     * @param executed_trades Pointer to modifiable vector
     */
    void init_trades_with_order(Order &order, std::vector<Trade> *executed_trades);

    /**
     * @brief removes filled orders from given map
     * @param map map to remove filled orders from
     */
    void compact_orderbook_helper(std::map<int32_t, std::vector<Order>> &map);

public:
    OrderBook();

    /**
     * @brief Adds order to order book, executes and returns possible trades
     * @param order Address of new order to be added to orderbook
     * @return Vector containing trades executed upon new order addition
     */
    std::vector<Trade> add_order(Order &order);

    /**
     * @brief Scans and removes order if it exists, to be used by traders with no access to Order object.
     * @param order_id order_id
     * @return True if order found and removed, false if order not found
     */
    bool remove_order(uint32_t order_id);

    /**
     * @brief Method used by internal algorithm with access to Order object, skips expensive lookup in order_lookup.
     * @param order order object to be deleted
     * @return True if order found and removed, false if order not found.
     */
    void mark_order_deleted(Order *order);

    /**
    * @brief Shows executed trades
    * @return Vector containing all executed trades
    */
    const std::vector<Trade>& show_trades() const;

    /**
     * @brief Removes filled/deleted orders from orderbook.
     */
    void compact_orderbook();
};