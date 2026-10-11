# The development tools (the linter, the license and vulnerability checkers and the mutation
# tester) are declared in tools.mod rather than go.mod, so a module that requires this one never
# sees them in its own dependency graph. Upgrade them with `make tools-upgrade`, never
# `go mod tidy -modfile=tools.mod`: tidy scans this module's own packages and would copy their
# dependencies into the tools file.
#
# TOOL runs a tool as the binary `go tool -n` builds, so a go.work the caller sets (GOWORK)
# still applies to the code the tool checks; -modfile itself can't be used in workspace mode.
TOOL = $$(GOWORK=off go tool -modfile=tools.mod -n $(1))

.PHONY: all clean lint test test-fuzz test-mutation test-race tools-upgrade vuln vuln-tools

all: clean lint test test-fuzz test-mutation test-race

clean:
	@rm -rf *.out test_mutation.json

lint:
	@gofmt_unformatted=$$(gofmt -l . 2>/dev/null | grep -v '^testdata/' || true); \
	test -z "$$gofmt_unformatted" || (echo "files not formatted:" && echo "$$gofmt_unformatted" && exit 1)
	go vet ./...
	go mod verify
	GOWORK=off go mod verify -modfile=tools.mod
	$(call TOOL,golangci-lint) run ./...
	$(call TOOL,go-licenses) check ./...
	@$(MAKE) --no-print-directory vuln
	@$(MAKE) --no-print-directory vuln-tools

vuln:
	@$(call TOOL,govulncheck) -test ./...

# The end-to-end test builds a rotini program, so it needs a Go toolchain and the module cache
# (or network); `go test -short ./...` skips it.
test:
	@go test -v -count=1 -coverprofile=test.out ./...
	@go tool cover -func=test.out | tail -1

# Fuzz target: FuzzArgv, in the root package. 60s in CI; override FUZZTIME (e.g.
# `make test-fuzz FUZZTIME=10m`) to dig deeper. When -fuzztime runs out, go test can lose a race
# with its own deadline and fail with a bare "context deadline exceeded" and no failing input;
# that outcome counts as a pass. Any other failure, or any run that writes a failing input, fails
# the target.
FUZZTIME ?= 60s

test-fuzz:
	@log=$$(mktemp); trap 'rm -f "$$log"' EXIT; \
	for target in FuzzArgv; do \
		if ! go test -list "^$$target$$" . 2>/dev/null | grep -q "^$$target$$"; then \
			echo "fuzz target $$target not found"; exit 1; \
		fi; \
		echo "→ $$target"; \
		go test -fuzz="^$$target$$" -fuzztime=$(FUZZTIME) -run=^$$ . >"$$log" 2>&1; status=$$?; \
		cat "$$log"; \
		if [ $$status -ne 0 ]; then \
			if grep -q '^    context deadline exceeded$$' "$$log" && ! grep -q 'Failing input written' "$$log"; then \
				echo "→ $$target: fuzz time elapsed; deadline race in go test, no failing input"; \
			else \
				exit 1; \
			fi; \
		fi; \
	done

test-mutation:
	@$(call TOOL,gremlins) unleash --config .gremlins.yaml

test-race:
	@go test -race -count=1 -coverprofile=test_race.out ./...
	@go tool cover -func=test_race.out | tail -1

# Scans each tool in tools.mod as a built binary: that is exactly the code that runs.
vuln-tools:
	@bin=$$(mktemp -d); trap 'rm -rf "$$bin"' EXIT; \
	for pkg in $$(GOWORK=off go list -modfile=tools.mod tool); do \
		name=$$(echo "$$pkg" | tr / _); \
		echo "→ $$pkg"; \
		GOWORK=off go build -modfile=tools.mod -o "$$bin/$$name" "$$pkg" || exit 1; \
		$(call TOOL,govulncheck) -mode=binary "$$bin/$$name" || exit 1; \
	done

# Upgrades every tool in tools.mod to its latest release. tools.mod is rebuilt from scratch, so it
# holds exactly what today's tools require; the module, go and toolchain lines are copied from
# go.mod. On any failure the previous tools.mod and tools.sum are restored. Review the diff and
# commit tools.mod and tools.sum together.
tools-upgrade:
	@tools=$$(GOWORK=off go list -modfile=tools.mod tool) || exit 1; \
	backup=$$(mktemp -d); cp tools.mod tools.sum "$$backup"/; \
	restore() { cp "$$backup"/tools.mod "$$backup"/tools.sum .; rm -rf "$$backup"; echo "tools-upgrade failed; tools.mod and tools.sum restored"; exit 1; }; \
	{ grep -E '^module ' go.mod; echo; grep -E '^go ' go.mod; echo; grep -E '^toolchain ' go.mod; } > tools.mod; \
	rm -f tools.sum; \
	GOWORK=off go get -modfile=tools.mod -tool $$(for t in $$tools; do printf '%s@latest ' "$$t"; done) || restore; \
	GOWORK=off go mod verify -modfile=tools.mod || restore; \
	rm -rf "$$backup"
	@$(MAKE) --no-print-directory vuln-tools
	@echo "tools upgraded; review: git diff tools.mod"
