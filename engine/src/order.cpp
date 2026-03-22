#include <cstdint>
#include <string>

#include "../include/order.hpp"

std::string side_to_string(const Side side) {
    if (side == Side::Buy) {
        return "Buy";
    } else {
        return "Sell";
    }
}