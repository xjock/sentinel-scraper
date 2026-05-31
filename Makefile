.PHONY: build run clean docker docker-run fmt vet bundle-assets package-windows package-linux package

ifeq ($(OS),Windows_NT)
    EXE := .exe
else
    EXE :=
endif

BINARY_NAME := sentinel-scraper
BINARY := $(BINARY_NAME)$(EXE)
OUTPUT := sentinel_data
VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

build:
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) .

run: build
ifeq ($(OS),Windows_NT)
	$(BINARY)
else
	./$(BINARY)
endif

clean:
ifeq ($(OS),Windows_NT)
	-if exist $(BINARY) del /Q $(BINARY)
	-if exist $(OUTPUT) rmdir /S /Q $(OUTPUT)
	-if exist dist rmdir /S /Q dist
	-if exist internal\bundle\assets_windows.tar.gz del /Q internal\bundle\assets_windows.tar.gz
else
	rm -rf $(BINARY) $(OUTPUT) dist internal/bundle/assets_windows.tar.gz
endif

docker:
	docker build -t $(BINARY_NAME) .

docker-run: docker
	docker run --rm -v $$(pwd)/$(OUTPUT):/app/$(OUTPUT) $(BINARY_NAME)

fmt:
	go fmt ./...

vet:
	go vet ./...

bundle-assets:
	cd internal/bundle/assets_windows && tar -czf ../assets_windows.tar.gz .

package-windows: bundle-assets
	mkdir -p dist
	go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_windows_amd64.exe .

package-linux:
	mkdir -p dist
	GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o dist/$(BINARY_NAME)_$(VERSION)_linux_amd64 .

package-appimage:
	bash packaging/linux/build-appimage.sh "$(VERSION)"

package: package-windows package-linux
