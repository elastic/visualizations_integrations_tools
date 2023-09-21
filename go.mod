module github.com/elastic/visualizations_integrations_tools

go 1.17

require gopkg.in/yaml.v2 v2.4.0 // indirect

require github.com/elastic/elastic-transport-go/v8 v8.0.0-20230329154755-1a3c63de0db6 // indirect

require (
	github.com/elastic/go-elasticsearch/v8 v8.10.0
	github.com/elastic/kbncontent v0.0.0
	sigs.k8s.io/yaml v1.3.0
)

replace github.com/elastic/kbncontent v0.0.0 => ../kbncontent
