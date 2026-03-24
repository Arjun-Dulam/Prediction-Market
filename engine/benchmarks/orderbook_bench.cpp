#include <benchmark/benchmark.h>

#include <chrono>
#include <iostream>
#include <memory>

#include "../include/exchange.hpp"
#include "../include/order.hpp"
#include "../include/orderbook.hpp"
#include "order_generator.hpp"

struct SharedOrderData {
  static constexpr size_t NUM_ORDERS = 15000000;

  std::vector<Order> no_match_orders;
  std::vector<Order> matching_orders;
  std::vector<size_t> shuffled_indices;

  static SharedOrderData& instance() {
    static SharedOrderData data;
    return data;
  }

 private:
  SharedOrderData() {
    OrderGenerator order_gen(MarketConfig{});

    no_match_orders.reserve(NUM_ORDERS);
    matching_orders.reserve(NUM_ORDERS);
    shuffled_indices.reserve(NUM_ORDERS);

    for (size_t i = 0; i < NUM_ORDERS; i++) {
      Order new_order = order_gen.generate_order();
      if (new_order.side == Side::Buy) {
        new_order.price -= 500;
      } else {
        new_order.price += 500;
      }
      no_match_orders.push_back(new_order);
    }

    for (size_t i = 0; i < NUM_ORDERS; i++) {
      Order new_order = order_gen.generate_order();
      matching_orders.push_back(new_order);
    }

    for (size_t i = 0; i < NUM_ORDERS; i++) {
      shuffled_indices.push_back(i);
    }
    std::mt19937 rng(67);
    std::shuffle(shuffled_indices.begin(), shuffled_indices.end(), rng);
  }
};

static void BM_AddOrder_No_Match(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  OrderBook order_book;
  OrderGenerator order_gen(MarketConfig{});

  for (int i = 0; i < state.range(0); i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price -= 500;
    } else {
      new_order.price += 500;
    }
    order_book.add_order(new_order);
  }

  size_t order_idx = 0;

  for (auto _ : state) {
    auto trade = order_book.add_order(
        shared.no_match_orders[shared.shuffled_indices[order_idx]]);
    benchmark::DoNotOptimize(trade);
    order_idx = (order_idx + 1) % SharedOrderData::NUM_ORDERS;
  }

  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_AddOrder_No_Match)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000);

static void BM_AddOrder_Latency(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  OrderBook order_book;
  OrderGenerator order_gen(MarketConfig{});

  std::vector<double> latencies;
  latencies.reserve(SharedOrderData::NUM_ORDERS);

  for (int i = 0; i < state.range(0); i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price -= 500;
    } else {
      new_order.price += 500;
    }
    order_book.add_order(new_order);
  }

  size_t order_idx = 0;

  for (auto _ : state) {
    auto start = std::chrono::high_resolution_clock::now();
    auto trade = order_book.add_order(
        shared.no_match_orders[shared.shuffled_indices[order_idx]]);
    auto end = std::chrono::high_resolution_clock::now();

    double dur = std::chrono::duration<double, std::nano>(end - start).count();
    latencies.push_back(dur);
    benchmark::DoNotOptimize(trade);
    order_idx = (order_idx + 1) % SharedOrderData::NUM_ORDERS;
  }

  std::sort(latencies.begin(), latencies.end());
  state.counters["p999"] =
      latencies[static_cast<size_t>(latencies.size() * 0.999)];
  state.counters["p99"] =
      latencies[static_cast<size_t>(latencies.size() * 0.99)];
  state.counters["p95"] =
      latencies[static_cast<size_t>(latencies.size() * 0.95)];
  state.counters["p50"] =
      latencies[static_cast<size_t>(latencies.size() * 0.50)];

  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_AddOrder_Latency)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000);

static void BM_RemoveOrder_VaryDepth(benchmark::State& state) {
  OrderBook order_book;
  MarketConfig cfg;
  OrderGenerator order_gen(cfg);
  const size_t depth = state.range(0);

  std::vector<uint32_t> orders_to_remove;
  orders_to_remove.reserve(depth);

  for (size_t i = 0; i < depth; i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price -= 500;
    } else {
      new_order.price += 500;
    }
    order_book.add_order(new_order);
    orders_to_remove.push_back(new_order.get_order_id());
  }

  std::mt19937 rng(67);
  std::shuffle(orders_to_remove.begin(), orders_to_remove.end(), rng);

  const size_t half = depth / 2;

  for (auto _ : state) {
    for (size_t i = 0; i < half; i++) {
      auto result = order_book.remove_order(orders_to_remove[i]);
      benchmark::DoNotOptimize(result);
    }
  }

  state.SetItemsProcessed(half);
}

BENCHMARK(BM_RemoveOrder_VaryDepth)
    ->Arg(1000)
    ->Iterations(1)
    ->Arg(10000)
    ->Iterations(1)
    ->Arg(100000)
    ->Iterations(1)
    ->Arg(1000000)
    ->Iterations(1)
    ->Arg(15000000)
    ->Iterations(1);

static void BM_MatchingPerformance(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  auto order_book = std::make_unique<OrderBook>();
  OrderGenerator order_gen(MarketConfig{});

  for (size_t i = 0; i < state.range(0); i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price += 50;
    } else {
      new_order.price -= 50;
    }
    order_book->add_order(new_order);
  }

  size_t orderIdx = 0;

  for (auto _ : state) {
    if (orderIdx >= SharedOrderData::NUM_ORDERS) {
      state.PauseTiming();
      orderIdx = 0;
      order_book = std::make_unique<OrderBook>();

      for (size_t i = 0; i < state.range(0); i++) {
        Order new_order = order_gen.generate_order();
        if (new_order.side == Side::Buy) {
          new_order.price += 50;
        } else {
          new_order.price -= 50;
        }
        order_book->add_order(new_order);
      }

      state.ResumeTiming();
    }

    auto trade = order_book->add_order(
        shared.matching_orders[shared.shuffled_indices[orderIdx]]);
    benchmark::DoNotOptimize(trade);

    orderIdx++;
  }
  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_MatchingPerformance)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000)
    ->Arg(25000000);

static void BM_MatchingLatency(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  auto order_book = std::make_unique<OrderBook>();
  OrderGenerator order_gen(MarketConfig{});

  std::vector<double> latencies;
  latencies.reserve(SharedOrderData::NUM_ORDERS);

  for (size_t i = 0; i < state.range(0); i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price += 50;
    } else {
      new_order.price -= 50;
    }
    order_book->add_order(new_order);
  }

  size_t orderIdx = 0;

  for (auto _ : state) {
    if (orderIdx >= SharedOrderData::NUM_ORDERS) {
      state.PauseTiming();
      orderIdx = 0;
      order_book = std::make_unique<OrderBook>();

      for (size_t i = 0; i < state.range(0); i++) {
        Order new_order = order_gen.generate_order();
        if (new_order.side == Side::Buy) {
          new_order.price += 50;
        } else {
          new_order.price -= 50;
        }
        order_book->add_order(new_order);
      }

      state.ResumeTiming();
    }

    auto start = std::chrono::high_resolution_clock::now();
    auto trade = order_book->add_order(
        shared.matching_orders[shared.shuffled_indices[orderIdx]]);
    auto end = std::chrono::high_resolution_clock::now();

    double dur = std::chrono::duration<double, std::nano>(end - start).count();
    latencies.push_back(dur);
    benchmark::DoNotOptimize(trade);

    orderIdx++;
  }

  std::sort(latencies.begin(), latencies.end());
  state.counters["p999"] =
      latencies[static_cast<size_t>(latencies.size() * 0.999)];
  state.counters["p99"] =
      latencies[static_cast<size_t>(latencies.size() * 0.99)];
  state.counters["p95"] =
      latencies[static_cast<size_t>(latencies.size() * 0.95)];
  state.counters["p50"] =
      latencies[static_cast<size_t>(latencies.size() * 0.50)];

  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_MatchingLatency)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000)
    ->Arg(25000000);

static void BM_Exchange_AddBook(benchmark::State& state) {
  Exchange exchange;
  size_t book_counter = 0;
  for (auto _ : state) {
    exchange.add_book("SYM" + std::to_string(book_counter++));
  }
  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_Exchange_AddBook);

static void BM_Exchange_AddOrder_No_Match(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  Exchange exchange;
  exchange.add_book("SYM");
  OrderGenerator order_gen(MarketConfig{});

  for (int i = 0; i < state.range(0); i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price -= 500;
    } else {
      new_order.price += 500;
    }
    exchange.add_order("SYM", new_order);
  }

  size_t order_idx = 0;
  for (auto _ : state) {
    auto result = exchange.add_order(
        "SYM", shared.no_match_orders[shared.shuffled_indices[order_idx]]);
    benchmark::DoNotOptimize(result);
    order_idx = (order_idx + 1) % SharedOrderData::NUM_ORDERS;
  }
  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_Exchange_AddOrder_No_Match)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000);

static void BM_Exchange_RemoveOrder(benchmark::State& state) {
  Exchange exchange;
  exchange.add_book("SYM");
  OrderGenerator order_gen(MarketConfig{});
  const size_t depth = state.range(0);

  std::vector<uint32_t> orders_to_remove;
  orders_to_remove.reserve(depth);

  for (size_t i = 0; i < depth; i++) {
    Order new_order = order_gen.generate_order();
    if (new_order.side == Side::Buy) {
      new_order.price -= 500;
    } else {
      new_order.price += 500;
    }
    exchange.add_order("SYM", new_order);
    orders_to_remove.push_back(new_order.get_order_id());
  }

  std::mt19937 rng(67);
  std::shuffle(orders_to_remove.begin(), orders_to_remove.end(), rng);

  const size_t half = depth / 2;

  for (auto _ : state) {
    for (size_t i = 0; i < half; i++) {
      auto result = exchange.remove_order("SYM", orders_to_remove[i]);
      benchmark::DoNotOptimize(result);
    }
  }
  state.SetItemsProcessed(half);
}

BENCHMARK(BM_Exchange_RemoveOrder)
    ->Arg(1000)
    ->Iterations(1)
    ->Arg(10000)
    ->Iterations(1)
    ->Arg(100000)
    ->Iterations(1)
    ->Arg(1000000)
    ->Iterations(1)
    ->Arg(15000000)
    ->Iterations(1);

static void BM_Exchange_Matching(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  Exchange exchange;
  exchange.add_book("SYM");
  OrderGenerator order_gen(MarketConfig{});

  auto refill_book = [&]() {
    exchange.remove_book("SYM");
    exchange.add_book("SYM");
    for (size_t i = 0; i < (size_t)state.range(0); i++) {
      Order new_order = order_gen.generate_order();
      if (new_order.side == Side::Buy) {
        new_order.price += 50;
      } else {
        new_order.price -= 50;
      }
      exchange.add_order("SYM", new_order);
    }
  };

  refill_book();
  size_t order_idx = 0;

  for (auto _ : state) {
    if (order_idx >= SharedOrderData::NUM_ORDERS) {
      state.PauseTiming();
      order_idx = 0;
      refill_book();
      state.ResumeTiming();
    }
    auto result = exchange.add_order(
        "SYM", shared.matching_orders[shared.shuffled_indices[order_idx]]);
    benchmark::DoNotOptimize(result);
    order_idx++;
  }
  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_Exchange_Matching)
    ->Arg(0)
    ->Arg(1000)
    ->Arg(10000)
    ->Arg(100000)
    ->Arg(1000000)
    ->Arg(15000000);

static void BM_Exchange_MultiSymbol_No_Match(benchmark::State& state) {
  auto& shared = SharedOrderData::instance();
  const int num_symbols = state.range(0);
  Exchange exchange;

  std::vector<std::string> symbols;
  symbols.reserve(num_symbols);
  for (int i = 0; i < num_symbols; i++) {
    symbols.push_back("SYM" + std::to_string(i));
    exchange.add_book(symbols.back());
  }

  size_t order_idx = 0;
  size_t sym_idx = 0;

  for (auto _ : state) {
    auto result = exchange.add_order(
        symbols[sym_idx],
        shared.no_match_orders[shared.shuffled_indices[order_idx]]);
    benchmark::DoNotOptimize(result);
    order_idx = (order_idx + 1) % SharedOrderData::NUM_ORDERS;
    sym_idx = (sym_idx + 1) % num_symbols;
  }
  state.SetItemsProcessed(state.iterations());
}

BENCHMARK(BM_Exchange_MultiSymbol_No_Match)
    ->Arg(1)
    ->Arg(10)
    ->Arg(100)
    ->Arg(1000)
    ->Arg(10000);

BENCHMARK_MAIN();
