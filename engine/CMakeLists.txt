cmake_minimum_required(VERSION 3.21)
project(StockExchange VERSION 1.0)

set(CMAKE_CXX_STANDARD 20)
set(CMAKE_CXX_STANDARD_REQUIRED ON)

if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE
      Release
      CACHE STRING "Choose the type of build." FORCE)
endif()

include(FetchContent)

FetchContent_Declare(
  benchmark
  GIT_REPOSITORY https://github.com/google/benchmark.git
  GIT_TAG v1.8.3)

FetchContent_Declare(
  googletest
  GIT_REPOSITORY https://github.com/google/googletest.git
  GIT_TAG v1.14.0)

set(BENCHMARK_ENABLE_TESTING
    OFF
    CACHE BOOL "" FORCE)
set(BENCHMARK_ENABLE_GTEST_TESTS
    OFF
    CACHE BOOL "" FORCE)
set(gtest_force_shared_crt
    ON
    CACHE BOOL "" FORCE)
FetchContent_MakeAvailable(benchmark googletest)

find_package(Threads REQUIRED)
find_package(gRPC CONFIG REQUIRED)
find_package(protobuf CONFIG REQUIRED)
find_package(absl CONFIG REQUIRED)

set(_PROTOBUF_PROTOC $<TARGET_FILE:protobuf::protoc>)
set(_GRPC_CPP_PLUGIN_EXECUTABLE $<TARGET_FILE:gRPC::grpc_cpp_plugin>)

set(PROTO_OUT ${CMAKE_BINARY_DIR}/proto)
file(MAKE_DIRECTORY ${PROTO_OUT})

get_filename_component(PROTO_FILE "${CMAKE_CURRENT_SOURCE_DIR}/../proto/exchange.proto" ABSOLUTE)
get_filename_component(PROTO_PATH "${PROTO_FILE}" PATH)

set(PROTO_SRCS ${PROTO_OUT}/exchange.pb.cc)
set(PROTO_HDRS ${PROTO_OUT}/exchange.pb.h)
set(GRPC_SRCS ${PROTO_OUT}/exchange.grpc.pb.cc)
set(GRPC_HDRS ${PROTO_OUT}/exchange.grpc.pb.h)

add_custom_command(
  OUTPUT ${PROTO_SRCS}
         ${PROTO_HDRS}
         ${GRPC_SRCS}
         ${GRPC_HDRS}
  COMMAND ${_PROTOBUF_PROTOC} ARGS --grpc_out=${PROTO_OUT} --cpp_out=${PROTO_OUT}
          --plugin=protoc-gen-grpc=${_GRPC_CPP_PLUGIN_EXECUTABLE} -I${PROTO_PATH} ${PROTO_FILE}
  DEPENDS ${PROTO_FILE}
  COMMENT "Generating gRPC/protobuf C++ files")

add_library(
  exchange_proto
  ${PROTO_SRCS}
  ${PROTO_HDRS}
  ${GRPC_SRCS}
  ${GRPC_HDRS})
target_include_directories(exchange_proto PUBLIC ${PROTO_OUT})
target_link_libraries(
  exchange_proto
  absl::absl_log
  gRPC::grpc++
  protobuf::libprotobuf)

set(ORDERBOOK_SOURCES src/orderbook.cpp src/order.cpp src/exchange.cpp)

set(BENCHMARK_SOURCES benchmarks/orderbook_bench.cpp benchmarks/order_generator.cpp)

add_library(OrderBookCompilation ${ORDERBOOK_SOURCES})

add_executable(OrderBookBenchmark ${BENCHMARK_SOURCES} ${ORDERBOOK_SOURCES})

target_link_libraries(OrderBookBenchmark benchmark::benchmark)
target_compile_options(
  OrderBookBenchmark
  PRIVATE -O3
          -march=native
          -DNDEBUG
          -flto)
target_link_options(OrderBookBenchmark PRIVATE -flto)

add_executable(OrderBookTests tests/test.cpp ${ORDERBOOK_SOURCES})
target_link_libraries(OrderBookTests GTest::gtest_main)
target_include_directories(OrderBookTests PRIVATE ${CMAKE_SOURCE_DIR}/include)
target_compile_options(
  OrderBookTests
  PRIVATE -g
          -O0
          -fsanitize=address
          -fsanitize=undefined)
target_link_options(
  OrderBookTests
  PRIVATE
  -fsanitize=address
  -fsanitize=undefined)

enable_testing()
add_test(NAME OrderBookTests COMMAND OrderBookTests)

add_executable(MatchingEngineServer src/grpc_server.cpp src/main_server.cpp ${ORDERBOOK_SOURCES})

target_include_directories(MatchingEngineServer PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/include)

target_link_libraries(
  MatchingEngineServer
  exchange_proto
  gRPC::grpc++
  protobuf::libprotobuf
  Threads::Threads)
