#pragma once

#include <string>
#include <cstdint>

enum class Side : uint8_t {
    Buy,
    Sell
};

class Order {
private:
    uint64_t timestamp;
    uint32_t order_id;
    friend class OrderBook;
    friend class OrderGenerator;
public:
    int32_t price;
    // Price is stored as cents, choosing to have two decimal places of precision.
    // Using signed int to accommodate negative prices for commodities such as what happened to oil in 2020.
    uint32_t quantity;
    Side side;
    bool deleted_or_filled = false;

    inline uint64_t get_timestamp() const {return timestamp;}
    inline uint32_t get_order_id() const {return order_id;}

    Order(int32_t p, uint32_t q, Side s, bool d_o_f) :
    timestamp(0), order_id(0), price(p), quantity(q), side(s), deleted_or_filled(d_o_f) {}
};

class Trade {
private:
    uint32_t trade_id;

    friend class OrderBook;
public:
    int32_t price;
    uint32_t quantity;
    uint32_t buy_order_id;
    uint32_t sell_order_id;

    inline uint32_t get_trade_id() const {return trade_id;}

    Trade(int32_t p, uint32_t q, uint32_t b_o_id, uint32_t s_o_id) :
    trade_id(0), price(p), quantity(q), buy_order_id(b_o_id), sell_order_id(s_o_id) {}
};

std::string side_to_string(const Side side);