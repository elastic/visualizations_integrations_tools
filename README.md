# legacy vis analyzer

Analyzes the usage of legacy visualization types in beats and integrations dashboard and indexes them into an Elasticsearch instance.

## Usage

* Install `yarn`
* Run `ES="<elasticsearch connection string>" node index.js`
* Import `dataview.ndjson` to have a bunch of runtime fields analyzing the structure
