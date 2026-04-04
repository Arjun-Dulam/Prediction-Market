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
using exchange_comms::OrderID;
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
                  OrderID* reply) override {
    Order local_order = to_engine_order(order_submission->order());
    exchange_.add_order(order_submission->symbol(), local_order);
    reply->set_order_id(local_order.get_order_id());

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
