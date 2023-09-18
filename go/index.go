package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"regexp"
	"log"

	"gopkg.in/yaml.v2"
	"github.com/elastic/go-elasticsearch/v8"
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
	hash string
	author string
	date string	
}

func getCommitData(path string) (CommitData, error) {
	dir, file := filepath.Split(path)
	gitCmd := fmt.Sprintf("cd %s && git log \"%s\"", dir, file)
	cmd := exec.Command("sh", "-c", gitCmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return CommitData{}, err
	}
	stdout := out.String()
	reg := regexp.MustCompile("commit (.+)\nAuthor: (.*)\nDate: (.*)")
	submatches := reg.FindSubmatch([]byte(stdout))
	commitData := CommitData{ hash: string(submatches[1]), author: strings.TrimSpace(string(submatches[2])), date: strings.TrimSpace(string(submatches[3])) }
	// fmt.Printf("%v", commitData)
	return commitData, nil
}

type Visualization struct {
	doc map[string]interface{}
	soType string
	app string
	source string
	link string
	dashboard string
	path string
	commit CommitData
	manifest map[string]interface{}
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
			doc:       doc,
			path:      visFilePath,
			soType:    folderName,
			app:       app,
			source:    source,
			link:      "by_reference",
			dashboard: dashboardTitle,
			commit:    commit,
		}
		visualizations = append(visualizations, visualization)
	}
	return visualizations
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
						visualization := Visualization{
							doc:       panelMap,
							soType:    panelType,
							app:       app,
							source:    source,
							link:      "by_value",
							dashboard: dashboard["attributes"].(map[string]interface{})["title"].(string),
							path:      dashboardFilePath,
							commit:    commit,
						}
						visualizations = append(visualizations, visualization)
					}
				case "lens", "map":
					embeddableConfig := panelMap["embeddableConfig"].(map[string]interface{})
					if _, ok := embeddableConfig["attributes"]; ok {
						visualization := Visualization{
							doc:       panelMap,
							soType:    panelType,
							app:       app,
							source:    source,
							link:      "by_value",
							dashboard: dashboard["attributes"].(map[string]interface{})["title"].(string),
							path:      dashboardFilePath,
							commit:    commit,
						}
						visualizations = append(visualizations, visualization)
					}
				}
			}	
		}
	}
	return visualizations, dashboards
}

func CollectIntegrationsContent(integrationsPath string) []Visualization {
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
			manifestContents, err := ioutil.ReadFile(manifestPath)
			if err != nil {
				fmt.Printf("Error reading manifest: %v\n", err)
				continue
			}
			var manifest map[string]interface{}
			if err := yaml.Unmarshal(manifestContents, &manifest); err != nil {
				fmt.Printf("Error parsing manifest: %v\n", err)
				continue
			}
			visualizations, dashboards := collectDashboardFolder(packageInfo.Name(), packagePath, "integration")
			for _, folderName := range []string{"visualization", "lens", "map", "search"} {
				visualizations = append(visualizations, collectVisualizationFolder(packageInfo.Name(), packagePath, "integration", dashboards, folderName)...)
			}
			fmt.Printf("Collected %d vis in %s\n", len(visualizations), packageInfo.Name())
			for _, vis := range visualizations {
				vis.manifest = manifest
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

func main() {
	// integrationData := CollectIntegrationsContent("../integrations")

	// for _, vis := range integrationData {
		// fmt.Printf("%v\n", vis)
	// }

	es, err := elasticsearch.NewClient(elasticsearch.Config{})

	if err != nil {
		fmt.Printf("problem generating elasticsearch client")
	}

	log.Println(es.Info())

	_, err = es.Indices.Delete([]string{"legacy_vis"}, es.Indices.Delete.WithAllowNoIndices(true))

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

	indexCreateRes, err := es.Indices.Create("legacy_vis", es.Indices.Create.WithBody(strings.NewReader(mapping)))

	if err != nil {
		fmt.Printf("Problem creating index: %v\n", err);
	}

	fmt.Printf("%v", indexCreateRes)
	// client.Indices.Create("my_index")
	
	// beatsData := collectBeats()
	// for _, vis := range beatsData {
	// 	fmt.Printf("%v\n", vis)
	// }
}
