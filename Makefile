GO_IMAGE ?= golang:1.26-bookworm
VERSION ?= 0.3.3
PLUGIN_DIR := build/plugins/linux/amd64
PLUGIN_SO := $(PLUGIN_DIR)/cliproxyapi-copilot.so
CACHE_DIR := .cache
VERSION_LDFLAG := -X main.pluginVersion=$(VERSION)

.PHONY: test build build-local package test-native clean

test:
	go test ./...

build:
	mkdir -p $(PLUGIN_DIR) $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod $(CACHE_DIR)/home
	docker run --rm --platform=linux/amd64 \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/src/$(CACHE_DIR)/home \
		-e GOCACHE=/src/$(CACHE_DIR)/go-build \
		-e GOMODCACHE=/src/$(CACHE_DIR)/go-mod \
		-v "$(CURDIR):/src" \
		-w /src \
		$(GO_IMAGE) \
		sh -ec 'CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags "$(VERSION_LDFLAG)" -buildmode=c-shared -o $(PLUGIN_SO) ./cmd/cliproxyapi-copilot'

build-local:
	mkdir -p $(PLUGIN_DIR)
	CGO_ENABLED=1 GOOS=linux GOARCH=amd64 go build -buildvcs=false -trimpath -ldflags "$(VERSION_LDFLAG)" -buildmode=c-shared -o $(PLUGIN_SO) ./cmd/cliproxyapi-copilot

package: build
	scripts/package-release.sh "$(VERSION)"

ifeq ($(shell uname -s),Linux)
test-native:
	mkdir -p $(CACHE_DIR)/go-build $(CACHE_DIR)/go-mod $(CACHE_DIR)/home
	docker run --rm --platform=linux/amd64 \
		--user "$$(id -u):$$(id -g)" \
		-e HOME=/src/$(CACHE_DIR)/home \
		-e GOENV=off \
		-e GOWORK=off \
		-e GOTOOLCHAIN=local \
		-e GOCACHE=/src/$(CACHE_DIR)/go-build \
		-e GOMODCACHE=/src/$(CACHE_DIR)/go-mod \
		-e NATIVE_HOST_GOOS=linux \
		-e NATIVE_HOST_GOARCH=amd64 \
		-v "$(CURDIR):/src" \
		-w /src \
		$(GO_IMAGE) scripts/prepare-native-host.sh
	NATIVE_HOST_GOOS=linux NATIVE_HOST_GOARCH=amd64 NATIVE_HOST_IMAGE="$(GO_IMAGE)" scripts/test-native-host.sh
else
test-native:
	scripts/prepare-native-host.sh
	NATIVE_HOST_IMAGE="$(GO_IMAGE)" scripts/test-native-host.sh
endif

clean:
	rm -rf build dist $(CACHE_DIR)
