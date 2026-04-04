#include <chrono>
#include <thread>

#include "../include/exchange.hpp"
#include "../include/order.hpp"
#include "../include/orderbook.hpp"
#include "gtest/gtest.h"

TEST(OrderTest, ConstructorInitializesFields) {
  Order order(10000, 100, Side::Buy);

  EXPECT_EQ(order.price, 10000);
  EXPECT_EQ(order.quantity, 100);
  EXPECT_EQ(order.side, Side::Buy);
  EXPECT_FALSE(order.get_tombstone());
}

TEST(OrderTest, SellOrderConstruction) {
  Order order(9500, 50, Side::Sell);

  EXPECT_EQ(order.price, 9500);
  EXPECT_EQ(order.quantity, 50);
  EXPECT_EQ(order.side, Side::Sell);
}

TEST(OrderTest, NegativePriceSupported) {
  Order order(-3700, 1000, Side::Sell);

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
  Order buy(10000, 100, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();
  EXPECT_TRUE(trades.empty());
  EXPECT_EQ(buy.get_order_id(), 0);
}

TEST_F(OrderBookTest, AddSellOrderToEmptyBook_NoMatch) {
  Order sell(10000, 100, Side::Sell);
  book.add_order(sell);
  const auto& trades = book.show_trades();
  EXPECT_TRUE(trades.empty());
  EXPECT_EQ(sell.get_order_id(), 0);
}

TEST_F(OrderBookTest, OrderIdsAreUnique) {
  Order o1(10000, 100, Side::Buy);
  Order o2(10100, 100, Side::Buy);
  Order o3(9900, 100, Side::Sell);

  book.add_order(o1);
  book.add_order(o2);
  book.add_order(o3);

  EXPECT_NE(o1.get_order_id(), o2.get_order_id());
  EXPECT_NE(o2.get_order_id(), o3.get_order_id());
  EXPECT_NE(o1.get_order_id(), o3.get_order_id());
}

TEST_F(OrderBookTest, TimestampsAreIncreasing) {
  Order o1(10000, 100, Side::Buy);
  Order o2(10100, 100, Side::Buy);

  book.add_order(o1);
  book.add_order(o2);

  EXPECT_LT(o1.get_timestamp(), o2.get_timestamp());
}

TEST_F(OrderBookTest, BuyMatchesBestAsk) {
  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, 10000);
  EXPECT_EQ(trades[0].quantity, 50);
  EXPECT_EQ(trades[0].buy_order_id, buy.get_order_id());
  EXPECT_EQ(trades[0].sell_order_id, sell.get_order_id());
}

TEST_F(OrderBookTest, SellMatchesBestBid) {
  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);

  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, 10000);
  EXPECT_EQ(trades[0].quantity, 50);
}

TEST_F(OrderBookTest, BuyMatchesLowerAsk) {
  Order sell(9900, 50, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, 9900);
}

TEST_F(OrderBookTest, SellMatchesHigherBid) {
  Order buy(10100, 50, Side::Buy);
  book.add_order(buy);

  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, 10100);
}

TEST_F(OrderBookTest, NoMatchWhenPricesDontOverlap) {
  Order sell(10100, 50, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, PartialFillBuyOrder) {
  Order sell(10000, 30, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].quantity, 30);
  EXPECT_EQ(buy.quantity, 20);
}

TEST_F(OrderBookTest, PartialFillSellOrder) {
  Order buy(10000, 30, Side::Buy);
  book.add_order(buy);

  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].quantity, 30);
  EXPECT_EQ(sell.quantity, 20);
}

TEST_F(OrderBookTest, MultipleFillsInOneOrder) {
  Order sell1(9900, 20, Side::Sell);
  Order sell2(10000, 30, Side::Sell);
  Order sell3(10100, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);
  book.add_order(sell3);

  Order buy(10100, 100, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 3);
  EXPECT_EQ(trades[0].price, 9900);
  EXPECT_EQ(trades[0].quantity, 20);
  EXPECT_EQ(trades[1].price, 10000);
  EXPECT_EQ(trades[1].quantity, 30);
  EXPECT_EQ(trades[2].price, 10100);
  EXPECT_EQ(trades[2].quantity, 50);
}

TEST_F(OrderBookTest, PriceTimePriority_SamePriceFIFO) {
  Order sell1(10000, 50, Side::Sell);
  Order sell2(10000, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].sell_order_id, sell1.get_order_id());
}

TEST_F(OrderBookTest, PriceTimePriority_BetterPriceFirst) {
  Order sell_expensive(10100, 50, Side::Sell);
  Order sell_cheap(9900, 50, Side::Sell);
  book.add_order(sell_expensive);
  book.add_order(sell_cheap);

  Order buy(10100, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, 9900);
  EXPECT_EQ(trades[0].sell_order_id, sell_cheap.get_order_id());
}

TEST_F(OrderBookTest, RemoveExistingOrderReturnsTrue) {
  Order order(10000, 100, Side::Buy);
  book.add_order(order);

  EXPECT_TRUE(book.remove_order(order.get_order_id()));
}

TEST_F(OrderBookTest, RemoveNonExistentOrderReturnsFalse) {
  EXPECT_FALSE(book.remove_order(99999));
}

TEST_F(OrderBookTest, RemovedOrderDoesNotMatch) {
  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);
  book.remove_order(sell.get_order_id());

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, RemoveOneOfMultipleSamePriceOrders) {
  Order sell1(10000, 50, Side::Sell);
  Order sell2(10000, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);

  book.remove_order(sell1.get_order_id());

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].sell_order_id, sell2.get_order_id());
}

TEST_F(OrderBookTest, DoubleRemoveReturnsFalse) {
  Order order(10000, 100, Side::Buy);
  book.add_order(order);

  EXPECT_TRUE(book.remove_order(order.get_order_id()));
  EXPECT_FALSE(book.remove_order(order.get_order_id()));
}

TEST_F(OrderBookTest, ShowTradesReturnsAllTrades) {
  Order sell1(10000, 50, Side::Sell);
  Order sell2(10100, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);

  Order buy1(10000, 50, Side::Buy);
  Order buy2(10100, 50, Side::Buy);
  book.add_order(buy1);
  book.add_order(buy2);

  const auto& all_trades = book.show_trades();
  EXPECT_EQ(all_trades.size(), 2);
}

TEST_F(OrderBookTest, TradesRecordedCorrectly) {
  Order sell1(10000, 50, Side::Sell);
  Order sell2(10100, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);

  Order buy(10100, 100, Side::Buy);
  book.add_order(buy);

  const auto& trades = book.show_trades();
  ASSERT_EQ(trades.size(), 2);
  EXPECT_EQ(trades[0].price, 10000);
  EXPECT_EQ(trades[1].price, 10100);
}

TEST_F(OrderBookTest, CompactionRemovesDeletedOrders) {
  Order sell1(10000, 50, Side::Sell);
  Order sell2(10000, 50, Side::Sell);
  book.add_order(sell1);
  book.add_order(sell2);

  book.remove_order(sell1.get_order_id());
  book.compact_orderbook();

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].sell_order_id, sell2.get_order_id());
}

TEST_F(OrderBookTest, OrderLookupWorksAfterCompaction) {
  Order o1(10000, 50, Side::Buy);
  Order o2(10000, 50, Side::Buy);
  Order o3(10000, 50, Side::Buy);
  book.add_order(o1);
  book.add_order(o2);
  book.add_order(o3);

  book.remove_order(o2.get_order_id());
  book.compact_orderbook();

  EXPECT_TRUE(book.remove_order(o1.get_order_id()));
  EXPECT_TRUE(book.remove_order(o3.get_order_id()));
}

TEST_F(OrderBookTest, MultipleCompactionCycles) {
  for (int i = 0; i < 3; ++i) {
    Order o1(10000, 50, Side::Buy);
    Order o2(10000, 50, Side::Buy);
    book.add_order(o1);
    book.add_order(o2);

    book.remove_order(o1.get_order_id());
    book.compact_orderbook();

    EXPECT_TRUE(book.remove_order(o2.get_order_id()));
  }
}

TEST_F(OrderBookTest, EmptyBookShowTradesReturnsEmpty) {
  const auto& trades = book.show_trades();
  EXPECT_TRUE(trades.empty());
}

TEST_F(OrderBookTest, CompactEmptyBookNoOp) {
  book.compact_orderbook();
  EXPECT_TRUE(book.show_trades().empty());
}

TEST_F(OrderBookTest, NegativePriceMatching) {
  Order sell(-100, 1000, Side::Sell);
  book.add_order(sell);

  Order buy(-100, 1000, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].price, -100);
}

TEST_F(OrderBookTest, LargeQuantityOrder) {
  Order sell(10000, UINT32_MAX / 2, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 1000, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  ASSERT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].quantity, 1000);
}

TEST_F(OrderBookTest, ManyOrdersSamePriceLevel) {
  std::vector<Order> sells;
  sells.reserve(100);
  for (int i = 0; i < 100; ++i) {
    sells.emplace_back(10000, 10, Side::Sell);
    book.add_order(sells.back());
  }

  Order buy(10000, 1000, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_EQ(trades.size(), 100);

  uint32_t total_qty = 0;
  for (const auto& t : trades) {
    total_qty += t.quantity;
  }
  EXPECT_EQ(total_qty, 1000);
}

TEST_F(OrderBookTest, AlternatingBuySellNoMatches) {
  for (int i = 0; i < 10; ++i) {
    Order buy(9000 + i * 10, 100, Side::Buy);
    Order sell(11000 + i * 10, 100, Side::Sell);

    book.add_order(buy);
    book.add_order(sell);
  }
  EXPECT_TRUE(book.show_trades().empty());
}

TEST_F(OrderBookTest, AggressorOrderQuantityUpdatedOnFullFill) {
  Order sell(10000, 50, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 50, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_EQ(buy.quantity, 0);
  EXPECT_EQ(trades.size(), 1);
}

TEST_F(OrderBookTest, AggressorOrderQuantityUpdatedOnPartialFill) {
  Order sell(10000, 100, Side::Sell);
  book.add_order(sell);

  Order buy(10000, 30, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_EQ(buy.quantity, 0);
  EXPECT_EQ(trades.size(), 1);
  EXPECT_EQ(trades[0].quantity, 30);
}

TEST_F(OrderBookTest, ManyOrdersWithRemovals) {
  const int NUM_ORDERS = 1000;
  std::vector<uint32_t> order_ids;
  order_ids.reserve(NUM_ORDERS);

  for (int i = 0; i < NUM_ORDERS; ++i) {
    Order order(10000 + (i % 100), 100, Side::Buy);
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
    Order sell(10000 + i, 10, Side::Sell);
    book.add_order(sell);
  }

  Order buy(10000 + NUM_ORDERS, 5000, Side::Buy);
  book.add_order(buy);
  const auto& trades = book.show_trades();

  EXPECT_EQ(trades.size(), (size_t)NUM_ORDERS);
}

class ExchangeTest : public ::testing::Test {
 protected:
  Exchange exchange;
};

TEST_F(ExchangeTest, AddBookAndRouteOrder) {
  exchange.add_book("AAPL");

  Order buy(15000, 100, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", buy));
}

TEST_F(ExchangeTest, AddOrderToNonExistentSymbolReturnsFalse) {
  Order buy(15000, 100, Side::Buy);
  EXPECT_FALSE(exchange.add_order("FAKE", buy));
}

TEST_F(ExchangeTest, RemoveOrderFromNonExistentSymbolReturnsFalse) {
  EXPECT_FALSE(exchange.remove_order("FAKE", 0));
}

TEST_F(ExchangeTest, AddDuplicateBookIsNoOp) {
  exchange.add_book("AAPL");

  Order sell(15000, 50, Side::Sell);
  exchange.add_order("AAPL", sell);

  exchange.add_book("AAPL");

  Order buy(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", buy));
}

TEST_F(ExchangeTest, RemoveBookThenAddOrderReturnsFalse) {
  exchange.add_book("AAPL");
  exchange.remove_book("AAPL");

  Order buy(15000, 100, Side::Buy);
  EXPECT_FALSE(exchange.add_order("AAPL", buy));
}

TEST_F(ExchangeTest, RemoveNonExistentBookDoesNotCrash) {
  exchange.remove_book("NONEXISTENT");
}

TEST_F(ExchangeTest, OrdersOnDifferentSymbolsDoNotMatch) {
  exchange.add_book("AAPL");
  exchange.add_book("GOOG");

  Order sell(15000, 50, Side::Sell);
  exchange.add_order("AAPL", sell);

  Order buy(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("GOOG", buy));

  Order aapl_buy(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", aapl_buy));
}

TEST_F(ExchangeTest, MultipleSymbolsIndependentMatching) {
  exchange.add_book("AAPL");
  exchange.add_book("GOOG");
  exchange.add_book("MSFT");

  Order aapl_sell(15000, 100, Side::Sell);
  Order goog_sell(28000, 50, Side::Sell);
  Order msft_sell(40000, 75, Side::Sell);

  EXPECT_TRUE(exchange.add_order("AAPL", aapl_sell));
  EXPECT_TRUE(exchange.add_order("GOOG", goog_sell));
  EXPECT_TRUE(exchange.add_order("MSFT", msft_sell));

  Order aapl_buy(15000, 100, Side::Buy);
  Order goog_buy(28000, 50, Side::Buy);
  Order msft_buy(40000, 75, Side::Buy);

  EXPECT_TRUE(exchange.add_order("AAPL", aapl_buy));
  EXPECT_TRUE(exchange.add_order("GOOG", goog_buy));
  EXPECT_TRUE(exchange.add_order("MSFT", msft_buy));
}

TEST_F(ExchangeTest, RemoveOrderFromCorrectSymbol) {
  exchange.add_book("AAPL");
  exchange.add_book("GOOG");

  Order aapl_order(15000, 100, Side::Buy);
  exchange.add_order("AAPL", aapl_order);
  std::this_thread::sleep_for(std::chrono::milliseconds(10));

  EXPECT_FALSE(exchange.remove_order("GOOG", aapl_order.get_order_id()));
  EXPECT_TRUE(exchange.remove_order("AAPL", aapl_order.get_order_id()));
}

TEST_F(ExchangeTest, RemoveOrderTwiceReturnsFalse) {
  exchange.add_book("AAPL");

  Order order(15000, 100, Side::Buy);
  exchange.add_order("AAPL", order);
  std::this_thread::sleep_for(std::chrono::milliseconds(10));

  EXPECT_TRUE(exchange.remove_order("AAPL", order.get_order_id()));
  EXPECT_FALSE(exchange.remove_order("AAPL", order.get_order_id()));
}

TEST_F(ExchangeTest, RemoveBookCleansUpOrders) {
  exchange.add_book("AAPL");

  Order order(15000, 100, Side::Buy);
  exchange.add_order("AAPL", order);

  exchange.remove_book("AAPL");

  EXPECT_FALSE(exchange.add_order("AAPL", order));
  EXPECT_FALSE(exchange.remove_order("AAPL", order.get_order_id()));
}

TEST_F(ExchangeTest, ReAddBookAfterRemovalStartsFresh) {
  exchange.add_book("AAPL");

  Order sell(15000, 50, Side::Sell);
  exchange.add_order("AAPL", sell);

  exchange.remove_book("AAPL");
  exchange.add_book("AAPL");

  Order buy(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", buy));
}

TEST_F(ExchangeTest, ManySymbols) {
  const int NUM_SYMBOLS = 100;

  for (int i = 0; i < NUM_SYMBOLS; ++i) {
    exchange.add_book("SYM" + std::to_string(i));
  }

  for (int i = 0; i < NUM_SYMBOLS; ++i) {
    Order order(10000 + i, 100, Side::Buy);
    EXPECT_TRUE(exchange.add_order("SYM" + std::to_string(i), order));
  }
}

TEST_F(ExchangeTest, EmptySymbolName) {
  exchange.add_book("");

  Order order(10000, 100, Side::Buy);
  EXPECT_TRUE(exchange.add_order("", order));
}

TEST_F(ExchangeTest, AddAndRemoveMultipleBooksRepeatedly) {
  for (int i = 0; i < 10; ++i) {
    exchange.add_book("AAPL");
    Order order(15000, 100, Side::Sell);
    EXPECT_TRUE(exchange.add_order("AAPL", order));
    exchange.remove_book("AAPL");
    EXPECT_FALSE(exchange.add_order("AAPL", order));
  }
}

TEST_F(ExchangeTest, OrderRoutedToCorrectBookProducesTrades) {
  exchange.add_book("AAPL");
  exchange.add_book("GOOG");

  Order sell(15000, 50, Side::Sell);
  exchange.add_order("AAPL", sell);

  Order buy(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", buy));

  Order buy2(15000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("AAPL", buy2));
}

TEST_F(ExchangeTest, RemoveBookWhileOtherBooksUnaffected) {
  exchange.add_book("AAPL");
  exchange.add_book("GOOG");

  Order aapl_sell(15000, 50, Side::Sell);
  Order goog_sell(28000, 50, Side::Sell);
  exchange.add_order("AAPL", aapl_sell);
  exchange.add_order("GOOG", goog_sell);

  exchange.remove_book("AAPL");

  Order goog_buy(28000, 50, Side::Buy);
  EXPECT_TRUE(exchange.add_order("GOOG", goog_buy));

  Order aapl_buy(15000, 50, Side::Buy);
  EXPECT_FALSE(exchange.add_order("AAPL", aapl_buy));
}

int main(int argc, char** argv) {
  ::testing::InitGoogleTest(&argc, argv);
  return RUN_ALL_TESTS();
}
