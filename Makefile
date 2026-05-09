APP := aphelion
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
CONFIG ?= $(HOME)/.aphelion/aphelion.toml
STATIC_BIN ?= $(BIN_DIR)/$(APP)-static
STATIC_TAGS ?= netgo osusergo sqlite_omit_load_extension
STATIC_LDFLAGS ?= -linkmode external -extldflags "-static"

.PHONY: build build-static run test check-config init install-user-service restart-user-service logs-user-service update install-release update-release paths gc docs-architecture architecture design-principles deadcode check-live-fixtures

build:
	mkdir -p $(BIN_DIR)
	go build -o $(BIN) .

build-static:
	mkdir -p $(dir $(STATIC_BIN))
	CGO_ENABLED=1 go build -tags '$(STATIC_TAGS)' -ldflags '$(STATIC_LDFLAGS)' -o $(STATIC_BIN) .
	./scripts/check-static-binary.sh $(STATIC_BIN)

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
	./scripts/check-design-principles.sh
	./scripts/check-no-live-child-fixtures.sh

architecture: docs-architecture
	go test . -run TestArchitectureImportBoundaries -count=1

design-principles:
	./scripts/check-design-principles.sh

deadcode:
	./scripts/check-deadcode.sh

check-live-fixtures:
	./scripts/check-no-live-child-fixtures.sh

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
