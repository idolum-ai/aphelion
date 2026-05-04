APP := aphelion
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
CONFIG ?= $(HOME)/.aphelion/aphelion.toml

.PHONY: build run test check-config init install-user-service restart-user-service logs-user-service update install-release update-release paths gc docs-architecture

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN) .

run: build
	./$(BIN) --config $(CONFIG)

test:
	go test ./...

check-config: build
	./$(BIN) --config $(CONFIG) --check-config

init: build
	./$(BIN) init --config $(CONFIG)

paths: build
	./$(BIN) paths --config $(CONFIG)

gc: build
	./$(BIN) gc --config $(CONFIG)

docs-architecture:
	./scripts/check-architecture-docs.sh

install-user-service: build
	./scripts/install-user-service.sh

restart-user-service:
	./$(BIN) park-restart --config $(CONFIG) --source make_restart
	systemctl --user restart $(APP)

logs-user-service:
	journalctl --user -u $(APP) -f

update:
	./scripts/update.sh

install-release:
	./scripts/install-release.sh

update-release:
	./scripts/update-release.sh
