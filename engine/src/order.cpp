#include "../include/order.hpp"

#include <cstdint>
#include <string>

std::string side_to_string(const Side side) {
  if (side == Side::Buy) {
    return "Buy";
  } else {
    return "Sell";
  }
}
