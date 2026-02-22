MODULE   := github.com/toska-mesh/toska-mesh
PROTO_DIR := ../toska-mesh-proto
PB_DIR   := pkg/meshpb
CMDS     := gateway discovery healthmonitor
BIN_DIR  := bin

export PATH := $(HOME)/go/bin:$(PATH)

.PHONY: all generate build test lint clean docker

all: generate build

# --- Protobuf ---

generate:
	protoc \
		--go_out=$(PB_DIR) --go_opt=module=$(MODULE)/$(PB_DIR) \
		--go-grpc_out=$(PB_DIR) --go-grpc_opt=module=$(MODULE)/$(PB_DIR) \
		--proto_path=$(PROTO_DIR) \
		$(PROTO_DIR)/discovery.proto

# --- Build ---

build: $(addprefix $(BIN_DIR)/,$(CMDS))

$(BIN_DIR)/%: cmd/%/main.go $(wildcard internal/**/*.go) $(wildcard pkg/**/*.go)
	@mkdir -p $(BIN_DIR)
	go build -o $@ ./cmd/$*

# --- Test ---

test:
	go test ./...

# --- Lint ---

lint:
	@command -v golangci-lint >/dev/null 2>&1 || { echo "golangci-lint not installed — skipping"; exit 0; }
	golangci-lint run ./...

# --- Docker ---

docker:
	$(foreach cmd,$(CMDS),docker build --build-arg CMD=$(cmd) -t toska-mesh-$(cmd) .;)

# --- Clean ---

clean:
	rm -rf $(BIN_DIR)
