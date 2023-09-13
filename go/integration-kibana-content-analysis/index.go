package analyzekbncontent

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

	"gopkg.in/yaml.v2"
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

func getCommitData(path string) (map[string]string, error) {
	dir, file := filepath.Split(path)
	gitCmd := fmt.Sprintf("cd %s && git log \"%s\"", dir, file)
	cmd := exec.Command("sh", "-c", gitCmd)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	stdout := out.String()
	reg := regexp.MustCompile("commit (.+)\nAuthor: (.*)\nDate: (.*)")
	submatches := reg.FindSubmatch([]byte(stdout))
	commitData := make(map[string]string)
	commitData["hash"] = string(submatches[1])
	commitData["author"] = strings.TrimSpace(string(submatches[2]))
	commitData["date"] = strings.TrimSpace(string(submatches[3]))
	// fmt.Printf("%v", commitData)
	return commitData, nil
}

func collectVisualizationFolder(app, path, source string, dashboards map[string]string, folderName string) []map[string]interface{} {
	visPath := filepath.Join(path, folderName)
	if _, err := os.Stat(visPath); os.IsNotExist(err) {
		return nil
	}
	visualizations := []map[string]interface{}{}
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
		visualization := map[string]interface{}{
			"doc":       doc,
			"path":      visFilePath,
			"commit":    commit,
		}
		visualizations = append(visualizations, visualization)
	}
	result := []map[string]interface{}{}
	for _, vis := range visualizations {
		doc := vis["doc"].(map[string]interface{})
		path := vis["path"].(string)
		commit := vis["commit"].(map[string]string)
		dashboardTitle, _ := dashboards[doc["id"].(string)]
		result = append(result, map[string]interface{}{
			"doc":       doc,
			"soType":    folderName,
			"app":       app,
			"source":    source,
			"link":      "by_reference",
			"dashboard": dashboardTitle,
			"path":      path,
			"commit":    commit,
		})
	}
	return result
}

func collectDashboardFolder(app, path, source string) ([]map[string]interface{}, map[string]string) {
	dashboardPath := filepath.Join(path, "dashboard")
	if _, err := os.Stat(dashboardPath); os.IsNotExist(err) {
		return nil, nil
	}
	visualizations := []map[string]interface{}{}
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
						visualization := map[string]interface{}{
							"doc":       panelMap,
							"soType":    panelType,
							"app":       app,
							"source":    source,
							"link":      "by_value",
							"dashboard": dashboard["attributes"].(map[string]interface{})["title"].(string),
							"path":      dashboardFilePath,
							"commit":    commit,
						}
						visualizations = append(visualizations, visualization)
					}
				case "lens", "map":
					embeddableConfig := panelMap["embeddableConfig"].(map[string]interface{})
					if _, ok := embeddableConfig["attributes"]; ok {
						visualization := map[string]interface{}{
							"doc":       panelMap,
							"soType":    panelType,
							"app":       app,
							"source":    source,
							"link":      "by_value",
							"dashboard": dashboard["attributes"].(map[string]interface{})["title"].(string),
							"path":      dashboardFilePath,
							"commit":    commit,
						}
						visualizations = append(visualizations, visualization)
					}
				}
			}	
		}
	}
	return visualizations, dashboards
}

func CollectIntegrationsContent(integrationsPath string) []map[string]interface{} {
	allVis := []map[string]interface{}{}
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
			visualizations, _ := collectDashboardFolder(packageInfo.Name(), packagePath, "integration")
			// for _, folderName := range []string{"visualization", "lens", "map", "search"} {
			// 	visualizations = append(visualizations, collectVisualizationFolder(packageInfo.Name(), packagePath, "integration", dashboards, folderName)...)
			// }
			fmt.Printf("Collected %d vis in %s\n", len(visualizations), packageInfo.Name())
			for _, vis := range visualizations {
				vis["manifest"] = manifest
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
	CollectIntegrationsContent("../integrations")
	// for _, vis := range integrationData {
	// 	fmt.Printf("%v\n", vis)
	// }

	// beatsData := collectBeats()
	// for _, vis := range beatsData {
	// 	fmt.Printf("%v\n", vis)
	// }
}
