#include <grpcpp/grpcpp.h>
#include <grpcpp/support/status.h>

#include <cstdint>
#include <iostream>
#include <memory>
#include <ostream>
#include <string>

#include "../include/exchange.hpp"
#include "../include/grpc_server.hpp"
#include "exchange.grpc.pb.h"
#include "exchange.pb.h"

using SYMBOL_NOT_FOUND = Exchange::SYMBOL_NOT_FOUND;
using exchange_comms::BookVoid;
using exchange_comms::Boolean;
using exchange_comms::OrderDeletion;
using exchange_comms::AddOrderResponse;
using exchange_comms::OrderSubmission;
using exchange_comms::Price;
using exchange_comms::Symbol;
using grpc::Server;
using grpc::ServerBuilder;
using grpc::ServerContext;
using grpc::Status;

class MatchingEngineServiceImpl final
    : public exchange_comms::MatchingEngine::Service {
 private:
  Exchange& exchange_;

  Order to_engine_order(auto grpc_order) {
    Side side = (grpc_order.side() == exchange_comms::Side::SIDE_BUY)
                    ? Side::Buy
                    : Side::Sell;
    Order new_order = Order(grpc_order.price(), grpc_order.quantity(), side);
    return new_order;
  }

 public:
  MatchingEngineServiceImpl(Exchange& exchange) : exchange_(exchange) {}
  Status AddBook(ServerContext* context, const Symbol* symbol,
                 BookVoid* reply) override {
    exchange_.add_book(symbol->symbol());
    return Status::OK;
  }

  Status RemoveBook(ServerContext* context, const Symbol* symbol,
                    BookVoid* reply) override {
    exchange_.remove_book(symbol->symbol());
    return Status::OK;
  }

  Status AddOrder(ServerContext* context,
                  const OrderSubmission* order_submission,
                  AddOrderResponse* reply) override {
    Order local_order = to_engine_order(order_submission->order());
    std::vector<Trade> trades;
    if (exchange_.add_order(order_submission->symbol(), local_order, &trades) == 0) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    }
    reply->set_order_id(local_order.get_order_id());
    for (const auto& trade : trades) {
      auto* result = reply->add_trades();
      result->set_buy_order_id(trade.buy_order_id);
      result->set_sell_order_id(trade.sell_order_id);
      result->set_price(trade.price);
      result->set_quantity(trade.quantity);
    }

    return Status::OK;
  }

  Status AddOrders(ServerContext*, const exchange_comms::OrderBatch* request,
                   exchange_comms::AddOrdersResponse* reply) override {
    std::vector<Exchange::Submission> orders;
    if (request->orders_size() < 1 || request->orders_size() > 32)
      return Status(grpc::StatusCode::INVALID_ARGUMENT, "batch size must be 1-32");
    for (const auto& input : request->orders()) {
      if (!input.has_order() || (input.order().side() != exchange_comms::SIDE_BUY &&
                                input.order().side() != exchange_comms::SIDE_SELL))
        return Status(grpc::StatusCode::INVALID_ARGUMENT, "invalid side/order");
      orders.push_back({input.symbol(), input.order().price(), input.order().quantity(),
                       input.order().side() == exchange_comms::SIDE_BUY ? Side::Buy : Side::Sell});
    }
    try {
      auto batch = exchange_.add_orders(orders);
      for (const auto& result : batch.results) {
        auto* output = reply->add_results();
        output->set_order_id(result.order_id);
        for (const auto& trade : result.trades) {
          auto* fill = output->add_trades();
          fill->set_buy_order_id(trade.buy_order_id);
          fill->set_sell_order_id(trade.sell_order_id);
          fill->set_price(trade.price);
          fill->set_quantity(trade.quantity);
        }
      }
      for (const auto& quote : batch.quotes) {
        auto* output = reply->add_quotes();
        output->set_symbol(quote.symbol);
        output->set_bid(quote.bid);
        output->set_ask(quote.ask);
      }
    } catch (const SYMBOL_NOT_FOUND&) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    } catch (const std::invalid_argument& error) {
      return Status(grpc::StatusCode::INVALID_ARGUMENT, error.what());
    }
    return Status::OK;
  }

  Status GetQuote(ServerContext*, const Symbol* symbol,
                  exchange_comms::BookQuote* reply) override {
    try {
      auto quote = exchange_.get_quote(symbol->symbol());
      reply->set_symbol(quote.symbol);
      reply->set_bid(quote.bid);
      reply->set_ask(quote.ask);
    } catch (const SYMBOL_NOT_FOUND&) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    }
    return Status::OK;
  }

  Status RemoveOrder(ServerContext* context,
                     const OrderDeletion* order_deletion,
                     Boolean* reply) override {
    bool bool_reply = exchange_.remove_order(order_deletion->symbol(),
                                             order_deletion->order_id());
    reply->set_boolean(bool_reply);
    return Status::OK;
  }

  Status GetBestBid(ServerContext* context, const Symbol* symbol,
                    Price* reply) override {
    int32_t best_bid;
    try {
      best_bid = exchange_.get_best_bid(symbol->symbol());
    } catch (const SYMBOL_NOT_FOUND&) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    }
    reply->set_price(best_bid);
    return Status::OK;
  }

  Status GetBestAsk(ServerContext* context, const Symbol* symbol,
                    Price* reply) override {
    int32_t best_ask;

    try {
      best_ask = exchange_.get_best_ask(symbol->symbol());
    } catch (const SYMBOL_NOT_FOUND&) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    }

    reply->set_price(best_ask);
    return Status::OK;
  }

  Status GetLastTradePrice(ServerContext* context, const Symbol* symbol,
                           Price* reply) override {
    int32_t last_trade_price;

    try {
      last_trade_price = exchange_.get_last_trade_price(symbol->symbol());
    } catch (const SYMBOL_NOT_FOUND&) {
      return Status(grpc::StatusCode::NOT_FOUND, "symbol not found");
    }

    reply->set_price(last_trade_price);
    return Status::OK;
  }
};

void RunServer() {
  std::string server_address("0.0.0.0:50051");
  Exchange exchange;
  MatchingEngineServiceImpl service(exchange);

  ServerBuilder builder;
  builder.AddListeningPort(server_address, grpc::InsecureServerCredentials());
  builder.RegisterService(&service);

  std::unique_ptr<Server> server(builder.BuildAndStart());
  std::cout << "Server listening on " << server_address << std::endl;
  server->Wait();
}
