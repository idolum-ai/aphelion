APP := aphelion
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
CONFIG ?= $(HOME)/.aphelion/aphelion.toml
STATIC_BIN ?= $(BIN_DIR)/$(APP)-static
STATIC_TAGS ?= netgo osusergo sqlite_omit_load_extension
STATIC_LDFLAGS ?= -linkmode external -extldflags "-static"
GOHOSTOS ?= $(shell go env GOHOSTOS 2>/dev/null || uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]' || echo unknown)
TEST_EXEC_TRUE ?= /usr/bin/true

.PHONY: build build-static run test verify-linux-compile test-compile check-config init install-user-service install-sandbox-net-helper restart-user-service logs-user-service update install-release update-release paths gc docs-architecture architecture public-readiness secrets design-principles deadcode check-live-fixtures taste

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
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion is Linux-only; 'make test' runs Linux runtime tests and cannot run on $(GOHOSTOS)." >&2; \
		echo "Run 'make verify-linux-compile' for a non-Linux compile-only check, or run 'make test' on Linux." >&2; \
		exit 1; \
	fi
	go test ./...

verify-linux-compile:
	GOOS=linux go test -exec $(TEST_EXEC_TRUE) ./...

test-compile: verify-linux-compile

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

architecture:
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion architecture checks are Linux-only and cannot run on $(GOHOSTOS)." >&2; \
		echo "Run 'make verify-linux-compile' for a non-Linux compile-only check, or run 'make architecture' on Linux." >&2; \
		exit 1; \
	fi
	$(MAKE) docs-architecture
	$(MAKE) public-readiness
	go test . -run TestArchitectureImportBoundaries -count=1

public-readiness:
	./scripts/check-public-readiness.sh

secrets:
	@command -v gitleaks >/dev/null 2>&1 || { echo "gitleaks is required for make secrets" >&2; exit 1; }
	gitleaks dir --redact .
	gitleaks git --redact --log-opts="--all" .

design-principles:
	./scripts/check-design-principles.sh

deadcode:
	./scripts/check-deadcode.sh

check-live-fixtures:
	./scripts/check-no-live-child-fixtures.sh

taste:
	./scripts/check-structural-taste.sh

install-user-service: build
	./scripts/install-user-service.sh

install-sandbox-net-helper: build
	./scripts/install-sandbox-net-helper.sh

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
