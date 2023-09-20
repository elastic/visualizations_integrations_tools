# visualizations integrations tools

This package currently has several functionalities:
* Analyzes the usage of legacy visualization types in beats and integrations dashboard and indexes them into an Elasticsearch instance.
* Inlines the visualizations in a given directory as by values panels
* Tracks the usage of @elastic/charts vs the Lens embeddable in the Kibana code base

## Prep

* Have locally installed node (tested with `16.14.2`)
* Init submodules using `git submodule update --init --recursive`
* Install dependencies using `yarn`

## Legacy vis analyzer usage

* [Install go](https://go.dev/doc/install)
* Run `ELASTICSEARCH_URL="<elasticsearch connection string>" go run index.go`
* Import `dataview.ndjson` to have a bunch of runtime fields analyzing the structure


## Code analyzer usage

* Run `ES="<elasticsearch connection string>" node index_kibana.js`

## Inliner usage


The inliner takes all "by reference" visualizations in a given directory, uses the provided running Kibana instance to migrate them to the latest version, migrates the dashboard saved object as well, then transforms the by-reference visualizations into by-value panels, deletes the visualization json files and updates the dashboard json files.

Important notes:
* Using the inliner script will make the dashboards incompatible with earlier versions of the stack - e.g. if it has been ran with a stack version 8.2, then the new dashboard json files will only work on version 8.2 and newer
* For old dashboards (prior to 7.10), some "agg based" visualizations might break if a target version of 7.17 or 8.0 is used. In these cases, please use at least a stack version of 8.1

* Run `KIBANA="<kibana connection string>" node inline.js <path to kibana folder>` (e.g. `./integrations/packages/system/kibana/`)
  * The kibana connection string has to include the password (for instances with security enabled) and the base path (for instances with configured base path), for example `KIBANA="http://elastic:changeme@localhost:5901/mgp"`
* Review changes in submodule repo
  * This review should include loading the dashboard into an instance with data to make sure everything is displayed properly
* If everything works fine, create PR
