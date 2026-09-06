FROM ubuntu:24.04 AS build

ENV DEBIAN_FRONTEND=noninteractive

RUN apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        ca-certificates \
        gpg \
        software-properties-common \
        wget \
    && add-apt-repository --yes ppa:ubuntu-toolchain-r/test \
    && wget -qO- https://apt.llvm.org/llvm-snapshot.gpg.key \
        | gpg --dearmor --yes --output /usr/share/keyrings/llvm-archive-keyring.gpg \
    && echo "deb [signed-by=/usr/share/keyrings/llvm-archive-keyring.gpg] https://apt.llvm.org/noble/ llvm-toolchain-noble-19 main" \
        > /etc/apt/sources.list.d/llvm.list \
    && apt-get update -qq \
    && apt-get install -y -qq --no-install-recommends \
        build-essential \
        clang-19 \
        cmake \
        file \
        git \
        libfuse3-dev \
        libssl-dev \
        libstdc++-14-dev \
        ninja-build \
        pkg-config \
        zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

COPY src /src

ENV CC=clang-19
ENV CXX=clang++-19

RUN cmake \
        -S /src \
        -B /build \
        -G Ninja \
        -DBUILD_SHARED_LIBS=OFF \
        -DCMAKE_BUILD_TYPE=Release \
        -DCMAKE_EXE_LINKER_FLAGS=-static \
        -DLIQUID_BUILD_BENCH=OFF \
        -DLIQUID_BUILD_FRONTENDS=OFF \
        -DLIQUID_BUILD_TESTS=OFF \
    && cmake --build /build --target liquid-cli \
    && strip /build/liquid \
    && file /build/liquid | grep -q 'statically linked'

FROM scratch
COPY --from=build /build/liquid /bin/liquid
