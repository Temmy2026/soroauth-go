# Makefile for the tasks CONTRIBUTING.md and ARCHITECTURE.md describe.
#
# Every target fails loudly: no recipe swallows an error, and targets that
# check something (fmt, vectors-check) exit non-zero when the check fails
# rather than printing a warning and moving on.
#
# Run `make` or `make help` for the list.

GO   ?= go
NPM  ?= npm
NODE ?= node

# The binary `make build` writes, relative to the repository root.
BIN_DIR := bin
BIN     := $(BIN_DIR)/soroauth

# Build metadata the CLI reports with --version. The release workflow overrides
# all three with the tag, the commit and the commit's date (see
# .github/workflows/release.yml); a local `make build` reports the checkout it
# was built from, which is what makes a bug report against a locally built
# binary actionable. Each falls back to a self-describing value rather than an
# empty string, so a source tarball with no git metadata still builds.
#
# BUILD_DATE is the commit's committer date, not the wall clock: the same
# commit always stamps the same timestamp, which is what makes two builds of the
# same tree byte-identical. See docs/reproducibility.md.
VERSION    ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT     ?= $(shell git rev-parse HEAD 2>/dev/null || echo unknown)
BUILD_DATE ?= $(shell git show -s --format=%cI $(COMMIT) 2>/dev/null || echo unknown)
SOURCE_DATE_EPOCH ?= $(shell git show -s --format=%ct $(COMMIT) 2>/dev/null || echo 0)
export SOURCE_DATE_EPOCH
LDFLAGS    := -s -w -X main.version=$(VERSION) -X main.commit=$(COMMIT) -X main.date=$(BUILD_DATE)

# -trimpath drops the build machine's paths and -buildvcs=false drops the VCS
# revision and dirty bit Go stamps by default. Both are the difference between
# a binary that is a function of the source and one that is a function of the
# checkout it was built in; the release workflow passes the same pair.
GOBUILDFLAGS := -trimpath -buildvcs=false

# How long `make fuzz` runs each target. The continuous workflow
# (.github/workflows/fuzz.yml) runs the same targets for much longer; this is
# the local smoke that proves a target starts and its seeds pass.
FUZZTIME ?= 30s

.PHONY: all help fmt vet test fuzz build server man md-check md-format vectors vectors-check demo-check diagrams diagrams-check e2e e2e-local clean parity parity-rust differential wasm wasm-check wasm-budget ts-test reproducible-check

# The default target runs exactly what a pull request has to pass before the
# golden-vector drift check, which needs Node and the network.
all: fmt vet test

help:
	@echo "targets:"
	@echo "  make all           fmt, vet and test (the default)"
	@echo "  make fmt           fail if any Go file is not gofmt-clean"
	@echo "  make vet           go vet ./..."
	@echo "  make test          go test ./..."
	@echo "  make fuzz          run every fuzz target for \$$(FUZZTIME) each (default 30s)"
	@echo "  make md-check      fail if any Markdown file is not prettier-clean"
	@echo "  make md-format     format every Markdown file in place"
	@echo "  make build         build the CLI to $(BIN)"
	@echo "  make server        build the verification service to $(BIN_DIR)/soroauth-server"
	@echo "  make man           build the CLI and emit its man page to $(BIN_DIR)/soroauth.1"
	@echo "  make vectors       regenerate testdata/vectors from the pinned reference libraries"
	@echo "  make vectors-check regenerate and fail if the committed vectors changed"
	@echo "  make diagrams      render docs/diagrams/*.dot to the committed SVGs"
	@echo "  make diagrams-check fail if the committed diagram SVGs have drifted"
	@echo "  make demo-check    check the browser demo's logic against the pinned SDK"
	@echo "  make e2e           build the test contract and run the live testnet suite"
	@echo "  make parity        run the Python stellar-sdk parity harness"
	@echo "  make parity-rust   run the Rust stellar-xdr parity harness"
	@echo "  make differential  regenerate the differential corpus and check it with JS and Python"
	@echo "  make wasm          build the js/wasm signing core into wasm/dist/"
	@echo "  make wasm-check    build the wasm core and prove it matches the golden vectors"
	@echo "  make wasm-budget   build the wasm core and fail if it is over its size ceiling"
	@echo "  make ts-test       typecheck and test the TypeScript wrapper package"
	@echo "  make reproducible-check build every release target twice and diff the checksums"
	@echo "  make clean         remove $(BIN_DIR)/ and build output"

# gofmt -l prints the files that need formatting; this target turns that output
# into a failure, which is what CI's gofmt step does.
fmt:
	@unformatted="$$(gofmt -l .)"; \
	if [ -n "$$unformatted" ]; then \
		echo "these files are not gofmt clean:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

vet:
	$(GO) vet ./...

# A local smoke over every fuzz target: each one must start, run its seed
# corpus and fuzz for FUZZTIME without finding a crash. The continuous
# workflow (.github/workflows/fuzz.yml) runs the same targets for much longer
# every night; a finding there files an issue rather than failing here.
# Override the budget per run: `make fuzz FUZZTIME=2m`.
fuzz:
	$(GO) test -run '^$$' -fuzz FuzzValidateDelegateOrder -fuzztime $(FUZZTIME) .
	$(GO) test -run '^$$' -fuzz FuzzInspect -fuzztime $(FUZZTIME) .
	$(GO) test -run '^$$' -fuzz FuzzPreimage -fuzztime $(FUZZTIME) .
	$(GO) test -run '^$$' -fuzz FuzzPayload -fuzztime $(FUZZTIME) .
	$(GO) test -run '^$$' -fuzz FuzzDecodeAuthorizationEntry -fuzztime $(FUZZTIME) .
	$(GO) test -run '^$$' -fuzz FuzzCopyRoundTrip -fuzztime $(FUZZTIME) ./internal/xdrcopy

# Markdown is normalised so a prose diff stays about content rather than about
# re-wrapping. The formatter, its version and its config are committed
# (package.json, .prettierrc.json, .prettierignore); run `npm ci` at the
# repository root first so `npx` resolves the pinned one.
md-check:
	npx prettier --check "**/*.md"

md-format:
	npx prettier --write "**/*.md"

build:
	$(GO) build $(GOBUILDFLAGS) -ldflags "$(LDFLAGS)" -o $(BIN) ./cmd/soroauth

# The verification service (docs/verification-service.md). It is a separate
# binary because serving verification over a network is a deployment choice;
# the published container image stays CLI-only. The version stamp feeds
# /healthz and `soroauth-server version`.
server:
	$(GO) build $(GOBUILDFLAGS) -ldflags "-s -w -X main.Version=$(VERSION)" -o $(BIN_DIR)/soroauth-server ./server

# The man page is generated by the binary itself, from the same command/flag
# table the completion scripts come from, so packaging never ships a page that
# describes a flag the CLI does not accept. It is written next to the binary
# rather than into the repository root, so `make clean` removes it and a stray
# build artefact cannot be committed.
man: build
	$(BIN) man --out $(BIN_DIR)/soroauth.1
	@echo "wrote $(BIN_DIR)/soroauth.1"

# Proves the documented build command is reproducible on this machine: it
# builds every release target twice from the same commit with
# scripts/reproduce.sh and fails if the two checksum lists differ. This is the
# local half of what .github/workflows/reproducible.yml checks against a real
# release. See docs/reproducibility.md.
reproducible-check:
	rm -rf $(BIN_DIR)/reproducible-a $(BIN_DIR)/reproducible-b
	bash scripts/reproduce.sh HEAD $(BIN_DIR)/reproducible-a
	bash scripts/reproduce.sh HEAD $(BIN_DIR)/reproducible-b
	@if ! diff -u $(BIN_DIR)/reproducible-a/checksums.txt $(BIN_DIR)/reproducible-b/checksums.txt; then \
		echo; \
		echo "Two builds of the same commit produced different bytes."; \
		echo "See docs/reproducibility.md for the sources of non-determinism"; \
		echo "this command neutralises, and check which one a change reintroduced."; \
		exit 1; \
	fi
	@echo "every target reproduced byte-for-byte"

# Golden vectors are generated, committed artefacts. Regenerating them is safe;
# `vectors-check` is the one that proves the committed files match, which is
# what CI runs.
#
# Two generators, two reference implementations: gen.mjs writes the
# authorization-entry vectors against @stellar/stellar-sdk, and gen-passkey.mjs
# writes the passkey signature-shape vectors against smart-account-kit. Both
# write under testdata/vectors, so one diff covers both.
vectors:
	cd testdata/gen && $(NPM) ci && $(NODE) gen.mjs && $(NODE) gen-passkey.mjs

vectors-check: vectors
	@if ! git diff --exit-code -- testdata/vectors; then \
		echo; \
		echo "The committed golden vectors differ from freshly generated ones."; \
		echo "Never edit a vector by hand. Commit the regenerated files with the"; \
		echo "generator change, or investigate why the reference output moved."; \
		exit 1; \
	fi

# The browser demo needs a browser, a platform authenticator and a deployed
# passkey wallet contract on testnet, so it cannot run end to end here. This
# runs everything about it that does not, against the SDK copy testdata/gen
# pins, so an API drift in the demo fails here instead of only in a browser.
# Needs `cd testdata/gen && npm ci` first.
demo-check:
	$(NODE) examples/browser-passkey/app.test.mjs

# The e2e suite needs the contract wasm built first, and stellar-cli to build
# it. Both failures are explicit rather than a confusing test error later.
e2e:
	@command -v stellar >/dev/null 2>&1 || { \
		echo "stellar-cli 28.0.0 is required to build the e2e test contract"; \
		exit 1; \
	}
	cd e2e/contracts && stellar contract build
	$(GO) test -tags e2e -v ./e2e/...

# The Python parity harness recomputes every vector's preimage and payload with
# a third implementation. It needs its own pinned SDK, so it runs in a venv the
# target creates rather than depending on the caller's environment.
parity:
	@command -v python3 >/dev/null 2>&1 || { \
		echo "python3 is required for the parity harness"; \
		exit 1; \
	}
	python3 -m venv .venv-parity
	. .venv-parity/bin/activate && \
		pip install -q -r testdata/parity-python/requirements.txt && \
		python3 testdata/parity-python/parity.py && \
		python3 testdata/parity-python/test_parity.py

# The differential fuzzing harness. cmd/difffuzz generates a deterministic
# corpus of random entries and records this library's preimage and payload for
# each; the JS verifier and the Python parity harness (pointed at the same
# corpus) then have to reproduce every one. See
# testdata/differential/README.md.
#
# Regenerating the corpus must not change the committed files: if it does, the
# generator moved and the regenerated corpus belongs in the same commit. The
# check mirrors vectors-check.
differential:
	@command -v python3 >/dev/null 2>&1 || { \
		echo "python3 is required for the differential harness"; \
		exit 1; \
	}
	go run ./cmd/difffuzz
	@if ! git diff --exit-code -- testdata/differential/corpus; then \
		echo; \
		echo "The committed differential corpus differs from freshly generated cases."; \
		echo "Never edit a case by hand. Commit the regenerated corpus with the"; \
		echo "generator change, or investigate why the recording moved."; \
		exit 1; \
	fi
	cd testdata/differential && $(NPM) ci && $(NODE) verify.mjs
	python3 -m venv .venv-parity
	. .venv-parity/bin/activate && \
		pip install -q -r testdata/parity-python/requirements.txt && \
		python3 testdata/parity-python/parity.py --vectors testdata/differential/corpus && \
		if ls testdata/differential/regressions/*.json >/dev/null 2>&1; then \
			python3 testdata/parity-python/parity.py --vectors testdata/differential/regressions; \
		fi

# The Rust parity harness recomputes every vector's preimage and payload with
# the stellar-xdr crate, the same XDR implementation the Soroban host uses. The
# crate is pinned exactly in Cargo.toml and Cargo.lock, and --locked makes the
# committed lock authoritative instead of letting cargo re-resolve.
parity-rust:
	@command -v cargo >/dev/null 2>&1 || { \
		echo "cargo (Rust 1.93.0) is required for the Rust parity harness"; \
		exit 1; \
	}
	cd testdata/parity-rust && cargo test --locked && cargo run --locked --bin parity

# The diagrams in docs/ are committed twice: the Graphviz source and the
# rendered SVG. `diagrams` rewrites the SVGs; `diagrams-check` fails if they no
# longer match their sources, which is what CI runs. Never edit an SVG by hand
# (docs/diagrams/README.md).
diagrams:
	node docs/diagrams/render.mjs

diagrams-check:
	node docs/diagrams/render.mjs --check

wasm:
	./wasm/build.sh

# Builds the module and then replays every golden vector through it, asserting
# byte-identical output.
wasm-check: wasm
	$(NODE) wasm/parity.mjs

ts-test: wasm
	cd wasm/ts && $(NPM) ci && $(NPM) run typecheck && $(NPM) test

# Builds the module and measures it against the 3 MiB ceiling, failing if it is
# over. The core is downloaded by a browser, so its size is a property worth
# failing on rather than noticing later.
wasm-budget: build wasm
	$(BIN_DIR)/soroauth wasm-budget --out wasm/dist/soroauth.wasm

clean:
	rm -rf $(BIN_DIR) wasm/dist wasm/ts/dist wasm/ts/node_modules .venv-parity
