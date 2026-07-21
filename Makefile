# This currently only works for OS X ARM64.
OCB_VERSION ?= 0.156.0

.PHONY: test
test:
	cd metricsgenreceiver && go test ./...

.PHONY: bench
bench:
	cd metricsgenreceiver && go test -run='^$$' -bench=BenchmarkMetricsGenReceiver \
	  -benchtime=1x -count=10 -timeout=20m -v .

# Micro-benchmarks use time-based benchtime: these are sub-microsecond operations
# that need many iterations for stable numbers. -benchtime=1x would give 1 sample.
.PHONY: bench-micro
bench-micro:
	cd metricsgenreceiver && go test -run='^$$' -bench=BenchmarkForEachDataPoint \
	  -benchtime=5s -benchmem -count=5 ./...

.PHONY: bench-profile
bench-profile:
	cd metricsgenreceiver && go test -run='^$$' \
	  -bench=BenchmarkMetricsGenReceiver/hostmetrics \
	  -benchtime=3x -cpuprofile=cpu.prof -memprofile=mem.prof -timeout=10m .
	@echo "CPU profile: go tool pprof -http=:8080 metricsgenreceiver/cpu.prof"
	@echo "Mem profile: go tool pprof -http=:8080 metricsgenreceiver/mem.prof"

.PHONY: install-ocb
install-ocb:
	curl --proto '=https' --tlsv1.2 -fL -o ocb \
	https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv$(OCB_VERSION)/ocb_$(OCB_VERSION)_darwin_arm64
	chmod +x ocb

.PHONY: build
build:
	./ocb --config builder-config.yaml

.PHONY: tidy
tidy:
	cd otelcol-dev && go mod tidy

# This install the otel collector with the name metricsgenreceiver in the GOPATH/bin directory.
.PHONY: install
install: build
	cp ./otelcol-dev/otelcol $(GOPATH)/bin/metricsgenreceiver

# Run the collector against the local ./otelcol.dev.yaml configuration file
.PHONY: run
run: install
	./otelcol-dev/otelcol --config ./otelcol.dev.yaml


.PHONY: clean
clean:
	rm -f ocb
	rm -rf ./otelcol-dev/*
