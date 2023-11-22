const crypto = require("crypto");
const fs = require("fs");
const axios = require("axios");

const baseUrl = process.env.KIBANA || "http://elastic:changeme@localhost:5601";
const folderPath = process.argv[2];

function cleanupAttributes(attributes) {
  if (
    attributes.kibanaSavedObjectMeta?.searchSourceJSON &&
    typeof attributes.kibanaSavedObjectMeta.searchSourceJSON !== "string"
  ) {
    attributes.kibanaSavedObjectMeta.searchSourceJSON = JSON.stringify(
      attributes.kibanaSavedObjectMeta.searchSourceJSON
    );
  }
  if (attributes.visState && typeof attributes.visState !== "string") {
    attributes.visState = JSON.stringify(attributes.visState);
  }
  if (attributes.uiStateJSON && typeof attributes.uiStateJSON !== "string") {
    attributes.uiStateJSON = JSON.stringify(attributes.uiStateJSON);
  }
  if (attributes.panelsJSON && typeof attributes.panelsJSON !== "string") {
    attributes.panelsJSON = JSON.stringify(attributes.panelsJSON);
  }
  if (attributes.optionsJSON && typeof attributes.optionsJSON !== "string") {
    attributes.optionsJSON = JSON.stringify(attributes.optionsJSON);
  }
  if (attributes.mapStateJSON && typeof attributes.mapStateJSON !== "string") {
    attributes.mapStateJSON = JSON.stringify(attributes.mapStateJSON);
  }
  if (
    attributes.layerListJSON &&
    typeof attributes.layerListJSON !== "string"
  ) {
    attributes.layerListJSON = JSON.stringify(attributes.layerListJSON);
  }
  return attributes;
}

function rehydrateAttributes(attributes) {
  if (
    attributes.kibanaSavedObjectMeta?.searchSourceJSON &&
    typeof attributes.kibanaSavedObjectMeta.searchSourceJSON === "string"
  ) {
    attributes.kibanaSavedObjectMeta.searchSourceJSON = JSON.parse(
      attributes.kibanaSavedObjectMeta.searchSourceJSON
    );
  }
  if (attributes.panelsJSON && typeof attributes.panelsJSON === "string") {
    attributes.panelsJSON = JSON.parse(attributes.panelsJSON);
  }
  if (attributes.optionsJSON && typeof attributes.optionsJSON === "string") {
    attributes.optionsJSON = JSON.parse(attributes.optionsJSON);
  }
  return attributes;
}

(async function () {
  const migratedVisualizations = await migrateSavedObjects("visualization");
  const migratedLens = await migrateSavedObjects("lens");
  const migratedMap = await migrateSavedObjects("map");
  const migratedSearch = await migrateSavedObjects("search");

  const dashboardPath = `${folderPath}/dashboard`;
  const dExists = fs.existsSync(dashboardPath);
  if (!dExists) throw new Error("no dashboard folder found");
  const dashboardPaths = fs.readdirSync(dashboardPath);
  const dashboards = dashboardPaths.map((d) =>
    JSON.parse(fs.readFileSync(`${dashboardPath}/${d}`, { encoding: "utf8" }))
  );
  const response3 = await axios.post(
    `${baseUrl}/api/saved_objects/_bulk_create?overwrite=true`,
    dashboards.map(
      ({ type, id, attributes, references, migrationVersion }) => ({
        type,
        id,
        attributes: cleanupAttributes(attributes),
        references,
        migrationVersion,
      })
    ),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  if (response3.data.saved_objects.some((s) => s.error)) {
    throw new Error("error loading dashboards");
  }
  const response4 = await axios.post(
    `${baseUrl}/api/saved_objects/_bulk_get`,
    dashboards.map((d) => ({ type: "dashboard", id: d.id })),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  let counter = {
    visualization: new Set(),
    lens: new Set(),
    map: new Set(),
    search: new Set(),
  };
  const inlinedDashboards = response4.data.saved_objects.map((d) => {
    console.log(`Processing dashboard ${d.attributes.title}`);
    const attributes = d.attributes;
    const references = d.references;
    const panels = JSON.parse(attributes.panelsJSON);
    panels.forEach((p) => {
      const ref =
        references.find(
          (r) => r.name === `${p.panelIndex}:panel_${p.panelIndex}`
        ) || references.find((r) => r.name === `${p.panelRefName}`);
      if (ref && migratedVisualizations.has(ref.id)) {
        const visToInline = migratedVisualizations.get(ref.id);
        const visState = JSON.parse(visToInline.attributes.visState);
        p.version = visToInline.migrationVersion.visualization;
        p.type = "visualization";
        p.embeddableConfig.savedVis = {
          title: visToInline.attributes.title,
          description: visToInline.attributes.description,
          uiState:
            typeof visToInline.attributes.uiStateJSON === "string"
              ? JSON.parse(visToInline.attributes.uiStateJSON)
              : visToInline.attributes.uiStateJSON,
          params: visState.params,
          type: visState.type,
          data: {
            aggs: !visState.aggs ? undefined : visState.aggs,
            searchSource: JSON.parse(
              visToInline.attributes.kibanaSavedObjectMeta.searchSourceJSON
            ),
          },
        };
        delete p.panelRefName;
        references.splice(references.indexOf(ref), 1);
        references.push(
          ...visToInline.references.map((r) => ({
            type: r.type,
            name: `${p.panelIndex}:${r.name}`,
            id: r.id,
          }))
        );
        console.log(
          `Inlined a vis, pushed ${visToInline.references.length} inner references`
        );
        counter.visualization.add(ref.id);
      } else if (ref && migratedLens.has(ref.id)) {
        const visToInline = migratedLens.get(ref.id);
        p.version = visToInline.migrationVersion.lens;
        p.type = "lens";
        p.embeddableConfig.attributes = {
          ...visToInline.attributes,
          references: visToInline.references,
        };
        delete p.panelRefName;
        references.splice(references.indexOf(ref), 1);
        references.push(
          ...visToInline.references.map((r) => ({
            type: r.type,
            name: `${p.panelIndex}:${r.name}`,
            id: r.id,
          }))
        );
        console.log(
          `Inlined a lens, pushed ${visToInline.references.length} inner references`
        );
        counter.lens.add(ref.id);
      } else if (ref && migratedMap.has(ref.id)) {
        const visToInline = migratedMap.get(ref.id);
        p.version = visToInline.migrationVersion.map;
        p.type = "map";
        p.embeddableConfig.attributes = {
          title: visToInline.attributes.title,
          description: visToInline.attributes.description,
          uiStateJSON: visToInline.attributes.uiStateJSON,
          mapStateJSON: visToInline.attributes.mapStateJSON,
          layerListJSON: visToInline.attributes.layerListJSON,
        };
        delete p.panelRefName;
        references.splice(references.indexOf(ref), 1);
        references.push(
          ...visToInline.references.map((r) => ({
            type: r.type,
            name: `${p.panelIndex}:${r.name}`,
            id: r.id,
          }))
        );
        console.log(
          `Inlined a map, pushed ${visToInline.references.length} inner references`
        );
        counter.map.add(ref.id);
      } else if (ref && migratedSearch.has(ref.id)) {
        const searchToInline = migratedSearch.get(ref.id);
        p.version = searchToInline.migrationVersion.search; // TODO - check this
        p.type = "search";
        p.title = searchToInline.title;
        p.description = searchToInline.description;
        p.embeddableConfig.attributes = {
          sort: searchToInline.attributes.sort,
          columns: searchToInline.attributes.columns,
          kibanaSavedObjectMeta:
            searchToInline.attributes.kibanaSavedObjectMeta,
          references: searchToInline.references,
        };
        delete p.panelRefName;
        references.splice(references.indexOf(ref), 1);
        references.push(
          ...searchToInline.references.map((r) => ({
            type: r.type,
            name: `${p.panelIndex}:${r.name}`,
            id: r.id,
          }))
        );
        console.log(
          `Inlined a search, pushed ${searchToInline.references.length} inner references`
        );
      } else {
        if (!ref) {
          if (p.type === undefined) {
            console.log(d.references);
            throw new Error("Could not match reference");
          }
          console.log(
            `Leaving panel of type ${p.type}, seems to be inlined already`
          );
        } else {
          console.log(`Leaving panel of type ${p.type}`);
        }
      }
    });
    attributes.panelsJSON = JSON.stringify(panels);
    return d;
  });
  console.log(`Inlined ${counter.visualization.size} visualizations`);
  console.log(`Inlined ${counter.map.size} maps`);
  console.log(`Inlined ${counter.lens.size} lenses`);
  if (counter.visualization.size !== migratedVisualizations.size) {
    console.log(
      `Some visualizations did not get inlined! ${counter.visualization.size}/${migratedVisualizations.size}`
    );
    [...migratedVisualizations.values()].map((v) => {
      if (!counter.visualization.has(v.id)) {
        console.log(`Did not inline ${v.id} anywhere`);
      }
    });
  }
  if (counter.map.size !== migratedMap.size) {
    console.log("Some maps did not get inlined!");
    [...migratedMap.values()].map((v) => {
      if (!counter.map.has(v.id)) {
        console.log(`Did not inline ${v.id} anywhere`);
      }
    });
  }
  if (counter.lens.size !== migratedLens.size) {
    console.log("Some lens did not get inlined!");
    [...migratedLens.values()].map((v) => {
      if (!counter.lens.has(v.id)) {
        console.log(`Did not inline ${v.id} anywhere`);
      }
    });
  }
  if (fs.existsSync(`${folderPath}/visualization`)) {
    console.log("Removing visualization folder");
    fs.rmSync(`${folderPath}/visualization`, { force: true, recursive: true });
  }
  if (fs.existsSync(`${folderPath}/map`)) {
    console.log("Removing maps folder");
    fs.rmSync(`${folderPath}/map`, { force: true, recursive: true });
  }
  if (fs.existsSync(`${folderPath}/lens`)) {
    console.log("Removing lens folder");
    fs.rmSync(`${folderPath}/lens`, { force: true, recursive: true });
  }
  console.log("Writing back dashboards");
  inlinedDashboards.forEach((d) => {
    fs.writeFileSync(
      `${dashboardPath}/${d.id}.json`,
      JSON.stringify(
        { ...d, attributes: rehydrateAttributes(d.attributes) },
        null,
        2
      )
    );
  });
  if (fs.existsSync("./result.json")) {
    fs.rmSync("./result.json");
  }
  fs.writeFileSync("./result.json", JSON.stringify(inlinedDashboards, null, 2));
})();

async function migrateSavedObjects(subFolder) {
  const visPath = `${folderPath}/${subFolder}`;
  const exists = fs.existsSync(visPath);
  if (!exists) return new Map();
  const visualizationPaths = fs.readdirSync(visPath);
  const visualizations = visualizationPaths.map((vis) =>
    JSON.parse(fs.readFileSync(`${visPath}/${vis}`, { encoding: "utf8" }))
  );

  const response = await axios.post(
    `${baseUrl}/api/saved_objects/_bulk_create?overwrite=true`,
    visualizations.map(
      ({ type, id, attributes, references, migrationVersion }) => ({
        type,
        id,
        attributes: cleanupAttributes(attributes),
        references,
        // sometimes the migration version is not set
        migrationVersion: migrationVersion || { [subFolder]: "7.0.0" },
      })
    ),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  if (response.data.saved_objects.some((s) => s.error)) {
    throw new Error(`error loading ${subFolder}`);
  }
  const response2 = await axios.post(
    `${baseUrl}/api/saved_objects/_bulk_get`,
    visualizations.map((v) => ({ type: subFolder, id: v.id })),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  const migratedVisualizations = new Map();
  response2.data.saved_objects.forEach((s) => {
    if (s.error) throw new Error(s.error);
    migratedVisualizations.set(s.id, s);
  });
  console.log(
    `Prepared ${response2.data.saved_objects.length} ${subFolder}s to be inlined`
  );
  return migratedVisualizations;
}
