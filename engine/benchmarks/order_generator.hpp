#pragma once

#include "../include/order.hpp"
#include <random>
#include <vector>
#include <cstdint>

struct MarketConfig {
    int32_t base_price = 10000;
    double price_std_dev = 100;
    double cancel_rate = .75;
    uint32_t min_quantity = 1;
    uint32_t max_quantity = 10000;
    double power_law_alpha = 2.5;
    double buy_sell_ratio = 0.5;
};

class OrderGenerator {
private:
    MarketConfig config;
    std::mt19937 rng;

    /**
    * Normal Distribution accurately models the clustering of prices near the true price
    * with exponentially rarer instances of prices farther away from the true price.
     */
    std::normal_distribution<double> price_dist;

    /**
     * Used as input for the power law function, which we use to model the differing quantities
     * of orders. Vast majority of orders are retail investors with small quantities while a small
     * subset of orders is institutional orders with quantities in the thousands.
     */
    std::uniform_real_distribution<double> uniform_dist;

    /**
     * Used to model the odds of an order being a Sell or a Buy.
     */
    std::bernoulli_distribution side_dist;

    /**
     * Used to model the odds of an order being cancelled after being placed.
     */
    std::bernoulli_distribution cancel_dist;

public:
    OrderGenerator(MarketConfig cfg, uint32_t seed = 67);

    Order generate_order();
    bool should_cancel();

private:
    int32_t generate_price();
    uint32_t generate_quantity();
    Side generate_side();
};
