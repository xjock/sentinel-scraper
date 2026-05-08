.PHONY: build run clean docker

BINARY := sentinel2-go
OUTPUT := sentinel2_data

build:
	go build -o $(BINARY) .

run: build
	./$(BINARY)

clean:
	rm -rf $(BINARY) $(OUTPUT)

docker:
	docker build -t $(BINARY) .

docker-run: docker
	docker run --rm -v $$(pwd)/$(OUTPUT):/app/$(OUTPUT) $(BINARY)

fmt:
	go fmt ./...

vet:
	go vet ./...
