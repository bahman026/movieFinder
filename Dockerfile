# Cross-compiles the Fyne GUI to a standalone Windows .exe.
#
# Fyne needs CGO, so the build stage pulls in the mingw-w64 toolchain and
# points the Go build at it. Nothing is installed on the host.
#
#   docker build --target export --output type=local,dest=dist .
#   -> dist/MovieFinder.exe
FROM golang:1.24-bookworm AS build

RUN apt-get update \
    && apt-get install -y --no-install-recommends \
        gcc-mingw-w64-x86-64 \
        g++-mingw-w64-x86-64 \
    && rm -rf /var/lib/apt/lists/*

# Any root CA dropped in certs/ becomes trusted inside the build. Needed when a
# debugging proxy or corporate appliance re-signs HTTPS — Windows trusts that CA,
# a fresh container does not. See certs/README.md and export-proxy-ca.ps1.
COPY certs/ /usr/local/share/ca-certificates/host/
RUN update-ca-certificates

# Module proxy. The default proxy.golang.org serves module zips from Google
# Cloud Storage, which geo-blocks some networks with
#   403 ... this service is not available in your location
# so a reachable mirror is used, falling back to fetching straight from the
# source repositories. Override with --build-arg GOPROXY=... if needed.
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src

# go.sum may not exist yet on a fresh checkout; the [m] glob keeps the COPY
# valid either way because go.mod always matches.
COPY go.mod go.su[m] ./
RUN go mod download || true

COPY . .
# Resolves the dependencies actually imported and writes go.sum. Copy
# dist/go.sum back into the repo after the first build to pin the versions.
#
# `go get ./...` rather than `go mod tidy`: tidy also walks the test-only
# dependencies of every dependency, which pulls in a long tail of modules this
# binary never links against and turns any mirror hiccup into a failed build.
# `-e` on the tidy pass keeps those irrelevant modules from being fatal.
RUN go get ./... && go mod tidy -e

ENV CGO_ENABLED=1 \
    GOOS=windows \
    GOARCH=amd64 \
    CC=x86_64-w64-mingw32-gcc \
    CXX=x86_64-w64-mingw32-g++

# -H windowsgui suppresses the console window behind the GUI.
#
# The cache mount keeps the compiled Fyne/GL objects between builds. Without it
# every build recompiles the whole CGO dependency tree, which takes minutes.
RUN --mount=type=cache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-H windowsgui -s -w" -o /out/MovieFinder.exe ./cmd/moviefinder

# Stage whose whole filesystem is written to the host by --output.
#
# go.mod comes back out alongside go.sum: `go get` above fills in the full
# indirect requirement list, and without exporting it the repo keeps only the
# minimal file, so nothing outside this image can build the project.
FROM scratch AS export
COPY --from=build /out/MovieFinder.exe /
COPY --from=build /src/go.mod /
COPY --from=build /src/go.sum /
