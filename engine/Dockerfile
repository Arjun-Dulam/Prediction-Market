FROM ubuntu:24.04 AS build
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    build-essential cmake libabsl-dev libgrpc++-dev libprotobuf-dev libssl-dev \
    protobuf-compiler protobuf-compiler-grpc && rm -rf /var/lib/apt/lists/*
WORKDIR /src
COPY CMakeLists.txt ./
COPY proto ./proto
COPY engine ./engine
RUN cmake -S . -B build -DCMAKE_BUILD_TYPE=Release \
    -DEXCHANGE_BUILD_TESTS=OFF -DEXCHANGE_BUILD_BENCHMARKS=OFF \
    && cmake --build build --target MatchingEngineServer -j2

FROM ubuntu:24.04
RUN apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    libabsl-dev libgrpc++-dev libprotobuf-dev libssl3 && rm -rf /var/lib/apt/lists/*
COPY --from=build /src/build/engine/MatchingEngineServer /usr/local/bin/matching-engine
EXPOSE 50051
ENTRYPOINT ["matching-engine"]
