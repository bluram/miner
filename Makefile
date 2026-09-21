.PHONY: build run test vet clean help

BINARY_NAME=miner
MAIN_PATH=./cmd/miner
BUILD_DIR=bin

help:
	@echo 'Usage:'
	@echo '  make <target>'
	@echo ''
	@echo 'Targets:'
	@echo '  build  - Build the binary into $(BUILD_DIR)/'
	@echo '  run    - Run the miner'
	@echo '  test   - Run tests'
	@echo '  vet    - Run go vet'
	@echo '  clean  - Remove build artifacts'

build:
	@mkdir -p $(BUILD_DIR)
	@go build -o $(BUILD_DIR)/$(BINARY_NAME) $(MAIN_PATH)
	@echo "built: $(BUILD_DIR)/$(BINARY_NAME)"

run:
	@go run $(MAIN_PATH) $(ARGS)

test:
	@go test ./...

vet:
	@go vet ./...

clean:
	@rm -rf $(BUILD_DIR)
