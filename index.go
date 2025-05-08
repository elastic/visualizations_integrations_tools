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
	"runtime"
	"sync/atomic"
	"time"

	"github.com/elastic/go-elasticsearch/v9"
	"github.com/elastic/go-elasticsearch/v9/esutil"
	"github.com/elastic/kbncontent"
	"sigs.k8s.io/yaml"
)

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
	Doc         map[string]interface{} `json:"doc"`
	SoType      string                 `json:"soType"`
	App         string                 `json:"app"`
	Source      string                 `json:"source"`
	Link        string                 `json:"link"`
	Dashboard   string                 `json:"dashboard"`
	Path        string                 `json:"path"`
	Commit      CommitData             `json:"commit"`
	Manifest    map[string]interface{} `json:"manifest"`
	GithubOwner string                 `json:"gh_owner"`
	VisType     string                 `json:"vis_type,omitempty"`
	TSVBType    string                 `json:"vis_tsvb_type,omitempty"`
	VisTitle    string                 `json:"vis_title,omitempty"`
	IsLegacy    bool                   `json:"is_legacy"`
	OwningGroup string                 `json:"owning_group"`
}

func getGithubOwner(manifest map[string]interface{}) string {
	if githubOwner, ok := manifest["owner"].(map[string]interface{})["github"].(string); ok {
		return githubOwner
	}

	return ""
}

func getOwningGroup(githubOwner string) string {
	if githubOwner == "" {
		return ""
	}

	if githubOwner == "elastic/security-external-integrations" || githubOwner == "elastic/security-asset-management" {
		return "security"
	} else if githubOwner == "elastic/ml-ui" {
		return "platform"
	} else {
		return "observability"
	}
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

		dashboardTitle, _ := dashboards[doc["id"].(string)]

		desc, _ := kbncontent.DescribeVisualizationSavedObject(doc)

		visualization := Visualization{
			Doc:       desc.Doc,
			SoType:    desc.SavedObjectType,
			Link:      desc.Link,
			VisType:   desc.Type(),
			TSVBType:  desc.TSVBType(),
			VisTitle:  desc.Title(),
			IsLegacy:  desc.IsLegacy(),
			Path:      visFilePath,
			App:       app,
			Source:    source,
			Dashboard: dashboardTitle,
			Commit:    commit,
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
		panels, err := kbncontent.DescribeByValueDashboardPanels(dashboard)
		if err != nil {
			fmt.Printf("Issue parsing dashboard panels for %s: %v\n", dashboardFilePath, err)
		}
		for _, panel := range panels {
			visualizations = append(visualizations, Visualization{
				Doc:       panel.Doc,
				SoType:    panel.SavedObjectType,
				Link:      panel.Link,
				VisType:   panel.Type(),
				TSVBType:  panel.TSVBType(),
				VisTitle:  panel.Title(),
				IsLegacy:  panel.IsLegacy(),
				App:       app,
				Source:    source,
				Dashboard: dashboard["attributes"].(map[string]interface{})["title"].(string),
				Path:      dashboardFilePath,
				Commit:    commit,
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
				vis.GithubOwner = getGithubOwner(manifest)
				vis.OwningGroup = getOwningGroup(vis.GithubOwner)
				allVis = append(allVis, vis)
			}
		}
	}
	return allVis
}

func CollectIntegrationsDataStreams(integrationsPath string) []map[string]interface{} {
	var allDataStreams []map[string]interface{}
	packages, err := os.ReadDir(filepath.Join(integrationsPath, "packages"))
	fmt.Printf("Collecting integrations\n")
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return allDataStreams
	}
	for _, packageInfo := range packages {
		if packageInfo.IsDir() {
			packagePath := filepath.Join(integrationsPath, "packages", packageInfo.Name())
			buildYmlPath := filepath.Join(packagePath, "_dev", "build", "build.yml")
			dataStreamPackagePath := filepath.Join(integrationsPath, "packages", packageInfo.Name(), "data_stream")
			dataStreams, err := os.ReadDir(dataStreamPackagePath)
			if err != nil {
				fmt.Printf("Error: %v\n", err)
			}
			integrationManifestPath := filepath.Join(packagePath, "manifest.yml")
			integrationManifest := collectManifest(integrationManifestPath)
			buildYml := collectManifest(buildYmlPath)
			// check whther the integration has a _dev/benchmark folder
			benchmarkPath := filepath.Join(packagePath, "_dev", "benchmark")
			if _, err := os.Stat(benchmarkPath); err == nil {
				// add flag to the integration manifest
				integrationManifest["has_benchmark"] = true
			} else {
				integrationManifest["has_benchmark"] = false
			}
			for _, dataStream := range dataStreams {
				manifestPath := filepath.Join(dataStreamPackagePath, dataStream.Name(), "manifest.yml")
				dataStreamManifest := collectManifest(manifestPath)
				// enrich data stream manifest with integration manifest
				dataStreamManifest["integration"] = integrationManifest
				dataStreamManifest["buildYml"] = buildYml
				// check whether the data streams has a _dev/test/pipeline folder
				pipelinePath := filepath.Join(dataStreamPackagePath, dataStream.Name(), "_dev", "test", "pipeline")
				if _, err := os.Stat(pipelinePath); err == nil {
					// add flag to the data stream manifest
					dataStreamManifest["has_pipeline_test"] = true
				} else {
					dataStreamManifest["has_pipeline_test"] = false
				}
				// same for system test
				systemTestPath := filepath.Join(dataStreamPackagePath, dataStream.Name(), "_dev", "test", "system")
				if _, err := os.Stat(systemTestPath); err == nil {
					dataStreamManifest["has_system_test"] = true
				} else {
					dataStreamManifest["has_system_test"] = false
				}
				allDataStreams = append(allDataStreams, dataStreamManifest)
			}
		}
	}
	return allDataStreams
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
			"vis_type": { "type": "keyword" },
			"vis_tsvb_type": { "type": "keyword" },
			"vis_title": { "type": "keyword" },
			"gh_owner": { "type": "keyword" },
			"owning_group": { "type": "keyword" },
			"is_legacy": { "type": "boolean" },
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

func saveVisualizationsToFile(visualizations []Visualization) {
	// Marshal the data into a JSON string
	jsonData, err := json.Marshal(visualizations)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return
	}

	// Define the file path
	filePath := "result.json"

	// Create or open the file for writing
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	// Write the JSON data to the file
	_, err = file.Write(jsonData)
	if err != nil {
		fmt.Println("Error writing JSON to file:", err)
		return
	}

	fmt.Printf("JSON data saved to %s\n", filePath)
}

func saveDataStreamsToFile(datastreams []map[string]interface{}) {
	// Marshal the data into a JSON string
	jsonData, err := json.Marshal(datastreams)
	if err != nil {
		fmt.Println("Error marshaling JSON:", err)
		return
	}

	// Define the file path
	filePath := "result_data_stream.json"

	// Create or open the file for writing
	file, err := os.Create(filePath)
	if err != nil {
		fmt.Println("Error creating file:", err)
		return
	}
	defer file.Close()

	// Write the JSON data to the file
	_, err = file.Write(jsonData)
	if err != nil {
		fmt.Println("Error writing JSON to file:", err)
		return
	}

	fmt.Printf("JSON data saved to %s\n", filePath)
}

func uploadDatastreams(datastreams []map[string]interface{}) {
	indexName := "integration_data_stream"

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
			"dynamic_templates": [
				{
				  "default_as_keyword": {
					"match_mapping_type": "*",
					"path_match": "*default",
					"runtime": {
					  "type": "keyword"
					}
				  }
				},
				{
				  "dynamic_as_keyword": {
					"match_mapping_type": "*",
					"path_match": "*mappings.dynamic",
					"runtime": {
					  "type": "keyword"
					}
				  }
				}
			]
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

	for i, vis := range datastreams {
		// Prepare the data payload: encode vis to JSON

		data, err := json.Marshal(vis)
		if err != nil {
			log.Fatalf("Cannot encode datastream %d: %s", i, err)
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
	visualizations := CollectIntegrationsVisualizations("./integrations")
	saveVisualizationsToFile(visualizations)
	uploadVisualizations(visualizations)
	dataStreams := CollectIntegrationsDataStreams("./integrations")
	saveDataStreamsToFile(dataStreams)
	uploadDatastreams(dataStreams)
}
