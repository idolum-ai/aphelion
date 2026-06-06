APP := aphelion
BIN_DIR := bin
BIN := $(BIN_DIR)/$(APP)
CONFIG ?= $(HOME)/.aphelion/aphelion.toml
STATIC_BIN ?= $(BIN_DIR)/$(APP)-static
STATIC_TAGS ?= netgo osusergo sqlite_omit_load_extension
STATIC_LDFLAGS ?= -linkmode external -extldflags "-static"
GOHOSTOS ?= $(shell go env GOHOSTOS 2>/dev/null || uname -s 2>/dev/null | tr '[:upper:]' '[:lower:]' || echo unknown)
TEST_EXEC_TRUE ?= /usr/bin/true
UNIT_TEST_PACKAGES := \
	./agent \
	./config \
	./core \
	./decision \
	./durableagent \
	./face \
	./githubapp \
	./governorauth \
	./governorbackend \
	./internal \
	./internal/decisionprojection \
	./internal/durabledefaults \
	./internal/maintenancecli \
	./internal/releaseinfo \
	./internal/standalonecli \
	./internal/stoplabels \
	./internal/telegramcommands \
	./internal/telegramcontrol \
	./internal/telegrampresentation \
	./internal/telegramruntime \
	./media \
	./memory \
	./openai \
	./pipeline \
	./principal \
	./prompt \
	./provider \
	./router \
	./runtime/doctor \
	./runtime/mission \
	./session \
	./telegram \
	./tool/sandbox \
	./turn \
	./voice \
	./workspace
CONTRACT_TEST_PATTERN := Test(ArchitectureImportBoundaries|DefinitionsIncludeNativeFileTools|FetchURL|NativeToolSchemasMatchRuntimeRequiredInputs|ReadFile|RootPackage|RunTurnDoesNotExecuteToolMissingFromDefinitions|ToolError|ToolLaneAllowlistsByRunKind|ToolManifestForRunKindFiltersConservativeLanes|ToolRegistryForRunKind)

.PHONY: build build-static run test test-unit test-contracts test-integration live-evals auto-evals verify-linux-compile test-compile init install-user-service install-sandbox-net-helper restart-user-service logs-user-service update install-release update-release docs-architecture architecture public-readiness secrets design-principles taste

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

test-unit:
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion is Linux-only; 'make test-unit' cannot run on $(GOHOSTOS)." >&2; \
		exit 1; \
	fi
	go test $(UNIT_TEST_PACKAGES)

test-contracts:
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion contract tests are Linux-only and cannot run on $(GOHOSTOS)." >&2; \
		exit 1; \
	fi
	$(MAKE) design-principles
	go test . ./agent ./runtime ./tool -run '$(CONTRACT_TEST_PATTERN)' -count=1

test-integration: test

live-evals:
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion live evals are Linux-only and cannot run on $(GOHOSTOS)." >&2; \
		exit 1; \
	fi
	APHELION_LIVE_EVAL=1 go test ./internal/standalonecli ./runtime -run 'TestLive(AgencySpectrumEvals|AutoPromptEvals|MissionAskClassifierEvals|ReflectionEvals)' -count=1

auto-evals:
	@if [ "$(GOHOSTOS)" != "linux" ]; then \
		echo "Aphelion auto evals are Linux-only and cannot run on $(GOHOSTOS)." >&2; \
		exit 1; \
	fi
	APHELION_LIVE_EVAL=1 go test ./internal/standalonecli ./runtime -run 'TestLive(AutoPromptEvals|MissionAskClassifierEvals)' -count=1

verify-linux-compile:
	GOOS=linux go test -exec $(TEST_EXEC_TRUE) ./...

test-compile: verify-linux-compile

init: build
	./$(BIN) init --config $(CONFIG)

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
	go test . -run 'Test(Architecture|RootPackage)' -count=1

public-readiness:
	./scripts/check-public-readiness.sh

secrets:
	@command -v gitleaks >/dev/null 2>&1 || { echo "gitleaks is required for make secrets" >&2; exit 1; }
	gitleaks dir --redact .
	gitleaks git --redact --log-opts="--all" .

design-principles:
	./scripts/check-design-principles.sh

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
