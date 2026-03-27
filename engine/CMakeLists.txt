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
find_package(gRPC REQUIRED)

find_program(PROTOC_EXE NAMES protoc REQUIRED)
find_program(GRPC_CPP_PLUGIN NAMES grpc_cpp_plugin REQUIRED)

set(PROTO_OUT ${CMAKE_BINARY_DIR}/proto)
file(MAKE_DIRECTORY ${PROTO_OUT})

set(PROTO_FILE ${CMAKE_CURRENT_SOURCE_DIR}/proto/exchange.proto)
set(PROTO_SRCS ${PROTO_OUT}/exchange.pb.cc)
set(PROTO_HDRS ${PROTO_OUT}/exchange.pb.h)
set(GRPC_SRCS ${PROTO_OUT}/exchange.grpc.pb.cc)
set(GRPC_HDRS ${PROTO_OUT}/exchange.grpc.pb.h)

add_custom_command(
  OUTPUT ${PROTO_SRCS}
         ${PROTO_HDRS}
         ${GRPC_SRCS}
         ${GRPC_HDRS}
  COMMAND ${PROTOC_EXE} ARGS --grpc_out=${PROTO_OUT} --cpp_out=${PROTO_OUT}
          --plugin=protoc-gen-grpc=${GRPC_CPP_PLUGIN} -I${CMAKE_CURRENT_SOURCE_DIR}/proto ${PROTO_FILE}
  DEPENDS ${PROTO_FILE}
  COMMENT "Generating gRPC/protobuf C++ files")

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

add_executable(
  MatchingEngineServer
  src/grpc_server.cpp
  src/main_server.cpp
  ${ORDERBOOK_SOURCES}
  ${PROTO_SRCS}
  ${GRPC_SRCS})

target_include_directories(MatchingEngineServer PRIVATE ${CMAKE_CURRENT_SOURCE_DIR}/include ${PROTO_OUT})

target_link_libraries(
  MatchingEngineServer
  gRPC::grpc++
  protobuf::libprotobuf
  Threads::Threads)
