module github.com/elastic/visualizations_integrations_tools

go 1.23

toolchain go1.24.3

require (
	github.com/elastic/elastic-transport-go/v8 v8.7.0 // indirect
	github.com/go-logr/logr v1.4.2 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/mitchellh/mapstructure v1.5.0 // indirect
	github.com/stretchr/objx v0.5.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel v1.35.0 // indirect
	go.opentelemetry.io/otel/metric v1.35.0 // indirect
	go.opentelemetry.io/otel/trace v1.35.0 // indirect
	golang.org/x/sys v0.19.0 // indirect
)

require (
	github.com/elastic/go-elasticsearch/v9 v9.0.0
	github.com/elastic/kbncontent v0.1.4
	sigs.k8s.io/yaml v1.4.0
)

replace github.com/elastic/kbncontent v0.0.0 => ../kbncontent
