# This currently only works for OS X ARM64.
.PHONY: install-ocb
install-ocb:
	curl --proto '=https' --tlsv1.2 -fL -o ocb \
	https://github.com/open-telemetry/opentelemetry-collector-releases/releases/download/cmd%2Fbuilder%2Fv0.134.0/ocb_0.134.0_darwin_arm64
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