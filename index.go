package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	
	"context"
	"github.com/elastic/go-elasticsearch/v8"
	"github.com/elastic/go-elasticsearch/v8/esutil"
	"sigs.k8s.io/yaml"
	"runtime"
	"sync/atomic"
	"time"
)

func cleanupDoc(doc map[string]interface{}) {
	if uiStateJSON, ok := doc["attributes"].(map[string]interface{})["uiStateJSON"].(string); ok {
		uiState := make(map[string]interface{})
		if err := json.Unmarshal([]byte(uiStateJSON), &uiState); err == nil {
			doc["attributes"].(map[string]interface{})["uiStateJSON"] = uiState
		}
	}
	if visState, ok := doc["attributes"].(map[string]interface{})["visState"].(string); ok {
		visStateMap := make(map[string]interface{})
		if err := json.Unmarshal([]byte(visState), &visStateMap); err == nil {
			doc["attributes"].(map[string]interface{})["visState"] = visStateMap
		}
	}
	if kibanaSavedObjectMeta, ok := doc["attributes"].(map[string]interface{})["kibanaSavedObjectMeta"].(map[string]interface{}); ok {
		if searchSourceJSON, ok := kibanaSavedObjectMeta["searchSourceJSON"].(string); ok {
			searchSource := make(map[string]interface{})
			if err := json.Unmarshal([]byte(searchSourceJSON), &searchSource); err == nil {
				kibanaSavedObjectMeta["searchSourceJSON"] = searchSource
			}
		}
	}
	if visState, ok := doc["attributes"].(map[string]interface{})["visState"].(map[string]interface{}); ok {
		if filter, ok := visState["params"].(map[string]interface{})["filter"]; ok {
			if _, ok := filter.(string); !ok {
				filterJSON, _ := json.Marshal(filter)
				visState["params"].(map[string]interface{})["filter"] = string(filterJSON)
			}
		}
		if series, ok := visState["params"].(map[string]interface{})["series"].([]interface{}); ok {
			for _, s := range series {
				if seriesMap, ok := s.(map[string]interface{}); ok {
					if filter, ok := seriesMap["filter"]; ok {
						if _, ok := filter.(string); !ok {
							filterJSON, _ := json.Marshal(filter)
							seriesMap["filter"] = string(filterJSON)
						}
					}
				}
			}
		}
	}
}

type CommitData struct {
	Hash   string `json:"hash"`
	Author string `json:"author"`
	Date   string `json:"date"`
}

func getCommitData(path string) (CommitData, error) {
	dir, file := filepath.Split(path)
	gitCmd := fmt.Sprintf("cd %s && git log --date=iso-strict \"%s\"", dir, file)
	cmd := exec.Command("sh", "-c", gitCmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return CommitData{}, err
	}
	stdout := out.String()
	reg := regexp.MustCompile("commit (.+)\nAuthor: (.*)\nDate: (.*)")
	submatches := reg.FindSubmatch([]byte(stdout))
	commitData := CommitData{Hash: string(submatches[1]), Author: strings.TrimSpace(string(submatches[2])), Date: strings.TrimSpace(string(submatches[3]))}
	return commitData, nil
}

type Visualization struct {
	Doc       map[string]interface{} 	`json:"doc"`
	SoType    string			`json:"soType"`
	App       string			`json:"app"`
	Source    string			`json:"source"`
	Link      string			`json:"link"`
	Dashboard string			`json:"dashboard"`
	Path      string			`json:"path"`
	Commit    CommitData			`json:"commit"`
	Manifest  map[string]interface{}	`json:"manifest"`
}

type PanelInfo struct {
	Doc       map[string]interface{} 	`json:"doc"`
	SoType    string			`json:"soType"`
	Link      string			`json:"link"`
}

func collectVisualizationFolder(app, path, source string, dashboards map[string]string, folderName string) []Visualization {
	visPath := filepath.Join(path, folderName)
	if _, err := os.Stat(visPath); os.IsNotExist(err) {
		return nil
	}
	var visualizations []Visualization
	files, err := ioutil.ReadDir(visPath)
	if err != nil {
		return nil
	}
	for _, file := range files {
		visFilePath := filepath.Join(visPath, file.Name())
		contents, err := ioutil.ReadFile(visFilePath)
		if err != nil {
			continue
		}
		var doc map[string]interface{}
		if err := json.Unmarshal(contents, &doc); err != nil {
			continue
		}
		commit, _ := getCommitData(visFilePath)
		cleanupDoc(doc)
		dashboardTitle, _ := dashboards[doc["id"].(string)]
		visualization := Visualization{
			Doc:       doc,
			Path:      visFilePath,
			SoType:    folderName,
			App:       app,
			Source:    source,
			Link:      "by_reference",
			Dashboard: dashboardTitle,
			Commit:    commit,
		}
		visualizations = append(visualizations, visualization)
	}
	return visualizations
}

func collectDashboardPanels(panelsJSON interface{}) (panelInfos []PanelInfo, err error) {
	var panels []interface{}
	switch panelsJSON.(type) {
	case string:
		json.Unmarshal([]byte(panelsJSON.(string)), &panels)
	default:
		panels = panelsJSON.([]interface{})
	}
	for _, panel := range panels {
		panelMap := panel.(map[string]interface{})

		switch panelType := panelMap["type"].(type) {
		default:
			// No op. There is no panel type, so this is by-reference.

		case string:
			switch panelType {
			case "visualization":
				embeddableConfig := panelMap["embeddableConfig"].(map[string]interface{})
				if _, ok := embeddableConfig["savedVis"]; ok {
					panelInfos = append(panelInfos, PanelInfo{
						Doc:       panelMap,
						SoType:    panelType,
						Link:      "by_value",
					})
				}
			case "lens", "map":
				embeddableConfig := panelMap["embeddableConfig"].(map[string]interface{})
				if _, ok := embeddableConfig["attributes"]; ok {
					panelInfos = append(panelInfos, PanelInfo{
						Doc:       panelMap,
						SoType:    panelType,
						Link:      "by_value",
					})
				}
			}
		}
	}
	return panelInfos, nil
}

func collectDashboardFolder(app, path, source string) ([]Visualization, map[string]string) {
	dashboardPath := filepath.Join(path, "dashboard")
	if _, err := os.Stat(dashboardPath); os.IsNotExist(err) {
		return nil, nil
	}
	var visualizations []Visualization
	dashboards := make(map[string]string)
	files, err := os.ReadDir(dashboardPath)
	if err != nil {
		fmt.Printf("Error reading dashboards directory: %v\n", err)
		return nil, nil
	}
	for _, file := range files {
		dashboardFilePath := filepath.Join(dashboardPath, file.Name())
		contents, err := ioutil.ReadFile(dashboardFilePath)
		if err != nil {
			fmt.Printf("Error reading dashboard file: %v\n", err)
			continue
		}
		var dashboard map[string]interface{}
		if err := json.Unmarshal(contents, &dashboard); err != nil {
			continue
		}
		commit, _ := getCommitData(dashboardFilePath)
		dashboardReferences := dashboard["references"].([]interface{})
		for _, reference := range dashboardReferences {
			ref := reference.(map[string]interface{})
			dashboards[ref["id"].(string)] = dashboard["attributes"].(map[string]interface{})["title"].(string)
		}
		panelsJSON := dashboard["attributes"].(map[string]interface{})["panelsJSON"]
		panels, _ := collectDashboardPanels(panelsJSON)
		for _, panel := range panels {
			visualizations = append(visualizations, Visualization{
				Doc: panel.Doc,
				SoType: panel.SoType,
				Link: panel.Link,
				App: app,
				Source: source,
				Dashboard: dashboard["attributes"].(map[string]interface{})["title"].(string),
				Path: dashboardFilePath,
				Commit: commit,
			})
		}
		
	}
	return visualizations, dashboards
}

// TODO I think some of this logic is superfluous. It may just serve to coerce the manifest into the correct type.
func collectManifest(manifestPath string) map[string]interface{} {
	manifestContents, err := ioutil.ReadFile(manifestPath)
	if err != nil {
		log.Printf("Error reading manifest: %v\n", err)
	}
	bytes, jsonErr := yaml.YAMLToJSON(manifestContents)
	if jsonErr != nil {
		log.Printf("Error converting manifest YAML to JSON: %v\n", jsonErr)
	}
	var manifest map[string]interface{}
	marshalErr := json.Unmarshal(bytes, &manifest)
	if marshalErr != nil {
		log.Printf("Error marshalling manifest JSON: %v\n", marshalErr)
	}

	return manifest
}

func CollectIntegrationsVisualizations(integrationsPath string) []Visualization {
	var allVis []Visualization
	packages, err := os.ReadDir(filepath.Join(integrationsPath, "packages"))
	fmt.Printf("Collecting integrations\n")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return allVis
	}
	for _, packageInfo := range packages {
		if packageInfo.IsDir() {
			packagePath := filepath.Join(integrationsPath, "packages", packageInfo.Name(), "kibana")
			manifestPath := filepath.Join(integrationsPath, "packages", packageInfo.Name(), "manifest.yml")
			manifest := collectManifest(manifestPath)

			visualizations, dashboards := collectDashboardFolder(packageInfo.Name(), packagePath, "integration")
			for _, folderName := range []string{"visualization", "lens", "map", "search"} {
				visualizations = append(visualizations, collectVisualizationFolder(packageInfo.Name(), packagePath, "integration", dashboards, folderName)...)
			}

			fmt.Printf("Collected %d vis in %s\n", len(visualizations), packageInfo.Name())

			for _, vis := range visualizations {
				vis.Manifest = manifest
				allVis = append(allVis, vis)
			}
		}
	}
	return allVis
}

// func collectBeats() []map[string]interface{} {
// 	allVis := []map[string]interface{}{}
// 	recurse := func(root string) {
// 		files, err := ioutil.ReadDir(root)
// 		if err != nil {
// 			return
// 		}
// 		for _, file := range files {
// 			if file.IsDir() && file.Name() == "7" {
// 				path := filepath.Join(root, "7")
// 				visualizations, dashboards := collectDashboardFolder(root, path, "beat")
// 				visualizations = append(visualizations, collectVisualizationFolder(root, path, "beat", dashboards, "visualization")...)
// 				visualizations = append(visualizations, collectVisualizationFolder(root, path, "beat", dashboards, "lens")...)
// 				visualizations = append(visualizations, collectVisualizationFolder(root, path, "beat", dashboards, "map")...)
// 				fmt.Printf("Collected %d vis in %s\n", len(visualizations), root)
// 				allVis = append(allVis, visualizations...)
// 			} else if file.IsDir() {
// 				recurse(filepath.Join(root, file.Name()))
// 			}
// 		}
// 	}
// 	recurse("./beats")
// 	return allVis
// }

func uploadVisualizations(visualizations []Visualization) {

	indexName := "legacy_vis"

	es, err := elasticsearch.NewClient(elasticsearch.Config{})

	if err != nil {
		fmt.Printf("problem generating elasticsearch client")
	}

	log.Println(es.Info())

	_, err = es.Indices.Delete([]string{indexName}, es.Indices.Delete.WithIgnoreUnavailable(true))

	if err != nil {
		fmt.Printf("Problem deleting old index %v", err)
	}

	mapping := `{
	    "mappings": {
		"properties": {
			"doc": { "type": "flattened", "depth_limit": 50 },
			"manifest": { "type": "flattened" },
			"soType": { "type": "keyword" },
			"app": { "type": "keyword" },
			"source": { "type": "keyword" }, 
			"link": { "type": "keyword" }, 
			"dashboard": { "type": "keyword" }, 
			"path": { "type": "keyword" },
			"commit": {
				"properties": {
					"hash": { "type": "keyword" },
					"author": { "type": "keyword" },
					"date": { "type": "date" }
				}
			}
		 }
	    }
	}`

	_, err = es.Indices.Create(indexName, es.Indices.Create.WithBody(strings.NewReader(mapping)))

	if err != nil {
		fmt.Printf("Problem creating index: %v\n", err)
	}

	bi, err := esutil.NewBulkIndexer(esutil.BulkIndexerConfig{
		Index:         indexName,
		Client:        es,
		NumWorkers:    runtime.NumCPU(),
		FlushBytes:    5e+6,
		FlushInterval: 30 * time.Second, // The periodic flush interval
	})

	if err != nil {
		log.Fatalf("Error creating the indexer: %s", err)
	}

	var countSuccessful uint64

	for i, vis := range visualizations {
		// Prepare the data payload: encode vis to JSON
		
		data, err := json.Marshal(vis)
		if err != nil {
			log.Fatalf("Cannot encode visualization %d: %s", i, err)
		}

		// >>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>>
		//
		// Add an item to the BulkIndexer
		//
		err = bi.Add(
			context.Background(),
			esutil.BulkIndexerItem{
				// Action field configures the operation to perform (index, create, delete, update)
				Action: "index",

				// DocumentID is the (optional) document ID
				// DocumentID: strconv.Itoa(a.ID),

				// Body is an `io.Reader` with the payload
				Body: bytes.NewReader(data),

				// OnSuccess is called for each successful operation
				OnSuccess: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem) {
					atomic.AddUint64(&countSuccessful, 1)
				},

				// OnFailure is called for each failed operation
				OnFailure: func(ctx context.Context, item esutil.BulkIndexerItem, res esutil.BulkIndexerResponseItem, err error) {
					if err != nil {
						log.Printf("ERROR: %s", err)
					} else {
						log.Printf("ERROR: %s: %s", res.Error.Type, res.Error.Reason)
					}
				},
			},
		)

		if err != nil {
			log.Fatalf("Unexpected error: %s", err)
		}
	}

	// Close the indexer
	if err := bi.Close(context.Background()); err != nil {
		log.Fatalf("Unexpected error: %s", err)
	}

	biStats := bi.Stats()

	log.Printf("%v", biStats)

	// beatsData := collectBeats()
	// for _, vis := range beatsData {
	// 	fmt.Printf("%v\n", vis)
	// }
}

func main() {
	visualizations := CollectIntegrationsVisualizations("../integrations")
	uploadVisualizations(visualizations)
}
