.PHONY: build clean vet run fmt

BINARY := waydesk
BUILD_DIR := bin
CMD_DIR := ./cmd/waydesk

# Build flags for production binary.
LDFLAGS := -s -w

build:
	@echo "━━━ Building $(BINARY)..."
	@mkdir -p $(BUILD_DIR)
	go build -ldflags "$(LDFLAGS)" -o $(BUILD_DIR)/$(BINARY) $(CMD_DIR)
	@echo "━━━ Built: $(BUILD_DIR)/$(BINARY)"

run: build
	@echo "━━━ Running $(BINARY)..."
	$(BUILD_DIR)/$(BINARY)

vet:
	go vet ./...

fmt:
	gofmt -s -w .

clean:
	rm -rf $(BUILD_DIR)
