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
        ca-certificates \
        clang-19 \
        clang-tools-19 \
        file \
        git \
        libfuse3-dev \
        libssl-dev \
        libstdc++-14-dev \
        ninja-build \
        pkg-config \
        wget \
        zlib1g-dev \
    && rm -rf /var/lib/apt/lists/*

# liquid uses cxx_std_26, which CMake knows from 3.30; Ubuntu 24.04 ships 3.28. Install the
# release liquid builds with, checked against the SHA-256 Kitware publishes for it. CMake scans
# C++20 and later sources for modules with clang-scan-deps, from clang-tools-19 above.
RUN CMAKE_VERSION=3.31.5 \
    && ARCH=$(uname -m) \
    && case "$ARCH" in \
        x86_64) CMAKE_SHA256=69118f4aafaf5f73e9897f2d39cf667a01d6fe8ef66f9a874e38996d7025e966 ;; \
        aarch64) CMAKE_SHA256=7d60f2d688cfc3b1b6797f9f192297d1d429771fffe8725cbc153bf0f36007c5 ;; \
        *) echo "no pinned CMake installer for $ARCH" >&2; exit 1 ;; \
    esac \
    && wget -qO /tmp/cmake.sh "https://github.com/Kitware/CMake/releases/download/v${CMAKE_VERSION}/cmake-${CMAKE_VERSION}-linux-${ARCH}.sh" \
    && echo "$CMAKE_SHA256  /tmp/cmake.sh" | sha256sum -c - \
    && sh /tmp/cmake.sh --skip-license --prefix=/usr/local \
    && rm /tmp/cmake.sh

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
        -DCMAKE_FIND_LIBRARY_SUFFIXES=.a \
    && cmake --build /build --target liquid-cli \
    && strip /build/liquid \
    && file /build/liquid | grep -q 'statically linked'

FROM scratch
COPY --from=build /build/liquid /bin/liquid
