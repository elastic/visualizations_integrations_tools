# legacy vis analyzer

This package has two functionalities:
* Analyzes the usage of legacy visualization types in beats and integrations dashboard and indexes them into an Elasticsearch instance.
* Inlines the visualizations in a given directory as by values panels

## Prep

* Have locally installed node (tested with `16.14.2`)
* Init submodules using `git submodule init`
* Install dependencies using `yarn`

## Analyzer usage

* Run `ES="<elasticsearch connection string>" node index.js`
* Import `dataview.ndjson` to have a bunch of runtime fields analyzing the structure

## Inliner usage

The inliner takes all "by reference" visualizations in a given directory, uses the provided running Kibana instance to migrate them to the latest version, migrates the dashboard saved object as well, then transforms the by-reference visualizations into by-value panels, deletes the visualization json files and updates the dashboard json files.

* Run `KIBANA="<kibana connection string>" node inline.js <path to kibana folder>` (e.g. `./integrations/packages/system/kibana/`)
  * The kibana connection string has to include the password (for instances with security enabled) and the base path (for instances with configured base path), for example `KIBANA="http://elastic:changeme@localhost:5901/mgp"`
* Review changes in submodule repo
  * This review should include loading the dashboard into an instance with data to make sure everything is displayed properly
* If everything works fine, create PR