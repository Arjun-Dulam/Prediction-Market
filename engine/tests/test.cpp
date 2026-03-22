#include "gtest/gtest.h"
#include "../include/orderbook.hpp"
#include "../include/order.hpp"

TEST(OrderTest, ConstructorInitializesFields) {
    Order order(10000, 100, Side::Buy, false);

    EXPECT_EQ(order.price, 10000);
    EXPECT_EQ(order.quantity, 100);
    EXPECT_EQ(order.side, Side::Buy);
    EXPECT_FALSE(order.deleted_or_filled);
}

TEST(OrderTest, SellOrderConstruction) {
    Order order(9500, 50, Side::Sell, false);

    EXPECT_EQ(order.price, 9500);
    EXPECT_EQ(order.quantity, 50);
    EXPECT_EQ(order.side, Side::Sell);
}

TEST(OrderTest, NegativePriceSupported) {
    Order order(-3700, 1000, Side::Sell, false);

    EXPECT_EQ(order.price, -3700);
}

TEST(OrderTest, SideToStringConversion) {
    EXPECT_EQ(side_to_string(Side::Buy), "Buy");
    EXPECT_EQ(side_to_string(Side::Sell), "Sell");
}

TEST(TradeTest, ConstructorInitializesFields) {
    Trade trade(10000, 50, 1, 2);

    EXPECT_EQ(trade.price, 10000);
    EXPECT_EQ(trade.quantity, 50);
    EXPECT_EQ(trade.buy_order_id, 1);
    EXPECT_EQ(trade.sell_order_id, 2);
}


class OrderBookTest : public ::testing::Test {
protected:
    OrderBook book;
};

TEST_F(OrderBookTest, AddBuyOrderToEmptyBook_NoMatch) {
    Order buy(10000, 100, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_TRUE(trades.empty());
    // Order ID is assigned (first ID is 0, which is valid)
    EXPECT_EQ(buy.get_order_id(), 0);
}

TEST_F(OrderBookTest, AddSellOrderToEmptyBook_NoMatch) {
    Order sell(10000, 100, Side::Sell, false);
    auto trades = book.add_order(sell);

    EXPECT_TRUE(trades.empty());
    EXPECT_EQ(sell.get_order_id(), 0);
}

TEST_F(OrderBookTest, OrderIdsAreUnique) {
    Order o1(10000, 100, Side::Buy, false);
    Order o2(10100, 100, Side::Buy, false);
    Order o3(9900, 100, Side::Sell, false);

    book.add_order(o1);
    book.add_order(o2);
    book.add_order(o3);

    EXPECT_NE(o1.get_order_id(), o2.get_order_id());
    EXPECT_NE(o2.get_order_id(), o3.get_order_id());
    EXPECT_NE(o1.get_order_id(), o3.get_order_id());
}

TEST_F(OrderBookTest, TimestampsAreIncreasing) {
    Order o1(10000, 100, Side::Buy, false);
    Order o2(10100, 100, Side::Buy, false);

    book.add_order(o1);
    book.add_order(o2);

    EXPECT_LT(o1.get_timestamp(), o2.get_timestamp());
}

TEST_F(OrderBookTest, BuyMatchesBestAsk) {
    Order sell(10000, 50, Side::Sell, false);
    book.add_order(sell);

    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, 10000);
    EXPECT_EQ(trades[0].quantity, 50);
    EXPECT_EQ(trades[0].buy_order_id, buy.get_order_id());
    EXPECT_EQ(trades[0].sell_order_id, sell.get_order_id());
}

TEST_F(OrderBookTest, SellMatchesBestBid) {
    Order buy(10000, 50, Side::Buy, false);
    book.add_order(buy);

    Order sell(10000, 50, Side::Sell, false);
    auto trades = book.add_order(sell);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, 10000);
    EXPECT_EQ(trades[0].quantity, 50);
}

TEST_F(OrderBookTest, BuyMatchesLowerAsk) {
    Order sell(9900, 50, Side::Sell, false);
    book.add_order(sell);

    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, 9900);  // Executes at resting order price
}

TEST_F(OrderBookTest, SellMatchesHigherBid) {
    // Buy at 101.00
    Order buy(10100, 50, Side::Buy, false);
    book.add_order(buy);

    // Sell at 100.00 should match the higher bid
    Order sell(10000, 50, Side::Sell, false);
    auto trades = book.add_order(sell);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, 10100);  // Executes at resting order price
}

TEST_F(OrderBookTest, NoMatchWhenPricesDontOverlap) {
    // Sell at 101.00
    Order sell(10100, 50, Side::Sell, false);
    book.add_order(sell);

    // Buy at 100.00 - no match (bid < ask)
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, PartialFillBuyOrder) {
    // Sell 30 shares at 100.00
    Order sell(10000, 30, Side::Sell, false);
    book.add_order(sell);

    // Buy 50 shares - only 30 will fill
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].quantity, 30);
    EXPECT_EQ(buy.quantity, 20);  // Remaining quantity
}

TEST_F(OrderBookTest, PartialFillSellOrder) {
    // Buy 30 shares at 100.00
    Order buy(10000, 30, Side::Buy, false);
    book.add_order(buy);

    // Sell 50 shares - only 30 will fill
    Order sell(10000, 50, Side::Sell, false);
    auto trades = book.add_order(sell);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].quantity, 30);
    EXPECT_EQ(sell.quantity, 20);  // Remaining quantity
}

TEST_F(OrderBookTest, MultipleFillsInOneOrder) {
    // Add multiple sells at different prices
    Order sell1(9900, 20, Side::Sell, false);
    Order sell2(10000, 30, Side::Sell, false);
    Order sell3(10100, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);
    book.add_order(sell3);

    // Buy 100 shares at 101.00 - should match all three
    Order buy(10100, 100, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 3);
    // Best price first
    EXPECT_EQ(trades[0].price, 9900);
    EXPECT_EQ(trades[0].quantity, 20);
    EXPECT_EQ(trades[1].price, 10000);
    EXPECT_EQ(trades[1].quantity, 30);
    EXPECT_EQ(trades[2].price, 10100);
    EXPECT_EQ(trades[2].quantity, 50);
}

TEST_F(OrderBookTest, PriceTimePriority_SamePriceFIFO) {
    // Add two sells at same price - first one should fill first
    Order sell1(10000, 50, Side::Sell, false);
    Order sell2(10000, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);

    // Buy enough to fill only one
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].sell_order_id, sell1.get_order_id());  // First order fills first
}

TEST_F(OrderBookTest, PriceTimePriority_BetterPriceFirst) {
    // Add sells at different prices
    Order sell_expensive(10100, 50, Side::Sell, false);
    Order sell_cheap(9900, 50, Side::Sell, false);
    book.add_order(sell_expensive);
    book.add_order(sell_cheap);

    // Buy should match cheaper one first
    Order buy(10100, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, 9900);
    EXPECT_EQ(trades[0].sell_order_id, sell_cheap.get_order_id());
}

// ============================================================================
// OrderBook - Order Removal Tests
// ============================================================================

TEST_F(OrderBookTest, RemoveExistingOrderReturnsTrue) {
    Order order(10000, 100, Side::Buy, false);
    book.add_order(order);

    EXPECT_TRUE(book.remove_order(order.get_order_id()));
}

TEST_F(OrderBookTest, RemoveNonExistentOrderReturnsFalse) {
    EXPECT_FALSE(book.remove_order(99999));
}

TEST_F(OrderBookTest, RemovedOrderDoesNotMatch) {
    // Add and remove a sell
    Order sell(10000, 50, Side::Sell, false);
    book.add_order(sell);
    book.remove_order(sell.get_order_id());

    // Buy should not match the removed sell
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, RemoveOneOfMultipleSamePriceOrders) {
    Order sell1(10000, 50, Side::Sell, false);
    Order sell2(10000, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);

    // Remove first sell
    book.remove_order(sell1.get_order_id());

    // Buy should match second sell
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].sell_order_id, sell2.get_order_id());
}

TEST_F(OrderBookTest, DoubleRemoveReturnsFalse) {
    Order order(10000, 100, Side::Buy, false);
    book.add_order(order);

    EXPECT_TRUE(book.remove_order(order.get_order_id()));
    EXPECT_FALSE(book.remove_order(order.get_order_id()));  // Already removed
}

// ============================================================================
// OrderBook - Trade History Tests
// ============================================================================

TEST_F(OrderBookTest, ShowTradesReturnsAllTrades) {
    Order sell1(10000, 50, Side::Sell, false);
    Order sell2(10100, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);

    Order buy1(10000, 50, Side::Buy, false);
    Order buy2(10100, 50, Side::Buy, false);
    book.add_order(buy1);
    book.add_order(buy2);

    const auto& all_trades = book.show_trades();
    EXPECT_EQ(all_trades.size(), 2);
}

TEST_F(OrderBookTest, TradesRecordedCorrectly) {
    Order sell1(10000, 50, Side::Sell, false);
    Order sell2(10100, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);

    Order buy(10100, 100, Side::Buy, false);
    book.add_order(buy);

    const auto& trades = book.show_trades();
    ASSERT_EQ(trades.size(), 2);
    // Verify trade details are correct
    EXPECT_EQ(trades[0].price, 10000);  // Best price first
    EXPECT_EQ(trades[1].price, 10100);
}

// ============================================================================
// OrderBook - Compaction Tests
// ============================================================================

TEST_F(OrderBookTest, CompactionRemovesDeletedOrders) {
    // Add orders
    Order sell1(10000, 50, Side::Sell, false);
    Order sell2(10000, 50, Side::Sell, false);
    book.add_order(sell1);
    book.add_order(sell2);

    // Remove first sell
    book.remove_order(sell1.get_order_id());

    // Compact
    book.compact_orderbook();

    // Second sell should still be matchable
    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].sell_order_id, sell2.get_order_id());
}

TEST_F(OrderBookTest, OrderLookupWorksAfterCompaction) {
    Order o1(10000, 50, Side::Buy, false);
    Order o2(10000, 50, Side::Buy, false);
    Order o3(10000, 50, Side::Buy, false);
    book.add_order(o1);
    book.add_order(o2);
    book.add_order(o3);

    // Remove middle order
    book.remove_order(o2.get_order_id());
    book.compact_orderbook();

    // Should still be able to remove o1 and o3
    EXPECT_TRUE(book.remove_order(o1.get_order_id()));
    EXPECT_TRUE(book.remove_order(o3.get_order_id()));
}

TEST_F(OrderBookTest, MultipleCompactionCycles) {
    for (int i = 0; i < 3; ++i) {
        Order o1(10000, 50, Side::Buy, false);
        Order o2(10000, 50, Side::Buy, false);
        book.add_order(o1);
        book.add_order(o2);

        book.remove_order(o1.get_order_id());
        book.compact_orderbook();

        EXPECT_TRUE(book.remove_order(o2.get_order_id()));
    }
}

// ============================================================================
// OrderBook - Edge Cases
// ============================================================================

TEST_F(OrderBookTest, EmptyBookShowTradesReturnsEmpty) {
    const auto& trades = book.show_trades();
    EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, CompactEmptyBookNoOp) {
    book.compact_orderbook();  // Should not crash
    EXPECT_TRUE(book.show_trades().empty());
}

TEST_F(OrderBookTest, NegativePriceMatching) {
    // Simulate negative oil prices
    Order sell(-100, 1000, Side::Sell, false);
    book.add_order(sell);

    Order buy(-100, 1000, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].price, -100);
}

TEST_F(OrderBookTest, LargeQuantityOrder) {
    Order sell(10000, UINT32_MAX / 2, Side::Sell, false);
    book.add_order(sell);

    Order buy(10000, 1000, Side::Buy, false);
    auto trades = book.add_order(buy);

    ASSERT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].quantity, 1000);
}

TEST_F(OrderBookTest, ManyOrdersSamePriceLevel) {
    // Add 100 orders at same price
    std::vector<Order> sells;
    sells.reserve(100);
    for (int i = 0; i < 100; ++i) {
        sells.emplace_back(10000, 10, Side::Sell, false);
        book.add_order(sells.back());
    }

    // Buy all 1000 shares
    Order buy(10000, 1000, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_EQ(trades.size(), 100);

    uint32_t total_qty = 0;
    for (const auto& t : trades) {
        total_qty += t.quantity;
    }
    EXPECT_EQ(total_qty, 1000);
}

TEST_F(OrderBookTest, AlternatingBuySellNoMatches) {
    // Buys below sells - no matches
    for (int i = 0; i < 10; ++i) {
        Order buy(9000 + i * 10, 100, Side::Buy, false);
        Order sell(11000 + i * 10, 100, Side::Sell, false);

        auto buy_trades = book.add_order(buy);
        auto sell_trades = book.add_order(sell);

        EXPECT_TRUE(buy_trades.empty());
        EXPECT_TRUE(sell_trades.empty());
    }
}

TEST_F(OrderBookTest, AggressorOrderQuantityUpdatedOnFullFill) {
    // The "aggressor" (incoming) order's quantity is updated during matching
    Order sell(10000, 50, Side::Sell, false);
    book.add_order(sell);

    Order buy(10000, 50, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_EQ(buy.quantity, 0);
    EXPECT_EQ(trades.size(), 1);
}

TEST_F(OrderBookTest, AggressorOrderQuantityUpdatedOnPartialFill) {
    Order sell(10000, 100, Side::Sell, false);
    book.add_order(sell);

    Order buy(10000, 30, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_EQ(buy.quantity, 0);
    EXPECT_EQ(trades.size(), 1);
    EXPECT_EQ(trades[0].quantity, 30);

}

TEST_F(OrderBookTest, ManyOrdersWithRemovals) {
    const int NUM_ORDERS = 1000;
    std::vector<uint32_t> order_ids;
    order_ids.reserve(NUM_ORDERS);

    // Add many orders
    for (int i = 0; i < NUM_ORDERS; ++i) {
        Order order(10000 + (i % 100), 100, Side::Buy, false);
        book.add_order(order);
        order_ids.push_back(order.get_order_id());
    }

    for (int i = 0; i < NUM_ORDERS; i += 2) {
        EXPECT_TRUE(book.remove_order(order_ids[i]));
    }

    book.compact_orderbook();

    for (int i = 1; i < NUM_ORDERS; i += 2) {
        EXPECT_TRUE(book.remove_order(order_ids[i]));
    }
}

TEST_F(OrderBookTest, HighVolumeMatching) {
    const int NUM_ORDERS = 500;

    for (int i = 0; i < NUM_ORDERS; ++i) {
        Order sell(10000 + i, 10, Side::Sell, false);
        book.add_order(sell);
    }

    Order buy(10000 + NUM_ORDERS, 5000, Side::Buy, false);
    auto trades = book.add_order(buy);

    EXPECT_EQ(trades.size(), NUM_ORDERS);

    const auto& all_trades = book.show_trades();
    EXPECT_EQ(all_trades.size(), NUM_ORDERS);
}

int main(int argc, char **argv) {
    ::testing::InitGoogleTest(&argc, argv);
    return RUN_ALL_TESTS();
}