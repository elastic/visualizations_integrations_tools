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
  const visPath = `${folderPath}/visualization`;
  const exists = fs.existsSync(visPath);
  if (!exists) throw new Error("No visualization folder found");
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
        migrationVersion,
      })
    ),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  if (response.data.saved_objects.some((s) => s.error)) {
    throw new Error("error loading visualizations");
  }
  const response2 = await axios.post(
    `${baseUrl}/api/saved_objects/_bulk_resolve`,
    visualizations.map((v) => ({ type: "visualization", id: v.id })),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  const migratedVisualizations = new Map();
  response2.data.resolved_objects.forEach((s) => {
    if (!s.outcome === "exactMatch") throw new Error();
    migratedVisualizations.set(s.saved_object.id, s.saved_object);
  });
  console.log(
    `Prepared ${response2.data.resolved_objects.length} visualizations to be inlined`
  );

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
    `${baseUrl}/api/saved_objects/_bulk_resolve`,
    dashboards.map((d) => ({ type: "dashboard", id: d.id })),
    {
      headers: {
        "kbn-xsrf": "abc",
      },
    }
  );
  let counter = new Set();
  const inlinedDashboards = response4.data.resolved_objects.map(
    ({ saved_object: d }) => {
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
          p.embeddableConfig.savedVis = {
            title: visToInline.attributes.title,
            description: visToInline.attributes.description,
            uiState: visToInline.attributes.uiStateJSON,
            params: visState.params,
            type: visState.type,
            data: {
              aggs: !visState.aggs ? undefined : visState.aggs,
              searchSource: JSON.parse(
                visToInline.attributes.kibanaSavedObjectMeta.searchSourceJSON
              ),
            },
          };
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
          counter.add(ref.id);
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
    }
  );
  console.log(`Inlined ${counter.size} visualizations`);
  if (counter.size !== response2.data.resolved_objects.length) {
    for (v in migratedVisualizations.values()) {
      if (!counter.has(v.id)) {
        console.log(`Did not inline ${v.id} anywhere`);
      }
    }
    throw new Error("Some visualizations did not get inlined!");
  }
  console.log("Removing visualization folder");
  fs.rmSync(visPath, { force: true, recursive: true });
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
