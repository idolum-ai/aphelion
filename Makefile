APP := aphelion
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
CONFIG ?= $(HOME)/.config/aphelion/config.toml

.PHONY: build run test install-user-service restart-user-service logs-user-service update install-release update-release

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN) .

run: build
	./$(BIN) --config $(CONFIG)

test:
	go test ./...

install-user-service: build
	./scripts/install-user-service.sh

restart-user-service:
	systemctl --user restart $(APP)

logs-user-service:
	journalctl --user -u $(APP) -f

update:
	./scripts/update.sh

install-release:
	./scripts/install-release.sh

update-release:
	./scripts/update-release.sh
