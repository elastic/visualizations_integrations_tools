const crypto = require("crypto");
const execSync = require("child_process").execSync;
const path = require("path");
const YAML = require("yaml");

const fs = require("fs");
const { Client } = require("@elastic/elasticsearch");
const client = new Client({
  node: process.env.ES || "http://elastic:changeme@localhost:9200",
});

function cleanupDoc(doc) {
  if (
    doc?.attributes?.uiStateJSON &&
    typeof doc.attributes.uiStateJSON === "string"
  ) {
    doc.attributes.uiStateJSON = JSON.parse(doc.attributes.uiStateJSON);
  }
  if (
    doc?.attributes?.visState &&
    typeof doc.attributes.visState === "string"
  ) {
    doc.attributes.visState = JSON.parse(doc.attributes.visState);
  }
  if (
    doc?.attributes?.kibanaSavedObjectMeta?.searchSourceJSON &&
    typeof doc.attributes.kibanaSavedObjectMeta.searchSourceJSON === "string"
  ) {
    doc.attributes.kibanaSavedObjectMeta.searchSourceJSON = JSON.parse(
      doc.attributes.kibanaSavedObjectMeta.searchSourceJSON
    );
  }
  if (
    doc?.attributes?.visState?.params?.filter &&
    typeof doc.attributes.visState.params.filter !== "string"
  ) {
    doc.attributes.visState.params.filter = JSON.stringify(
      doc.attributes.visState.params.filter
    );
  }
  if (
    doc?.attributes?.visState?.params?.series &&
    Array.isArray(doc.attributes.visState.params.series)
  ) {
    doc.attributes.visState.params.series =
      doc.attributes.visState.params.series.map((s) => ({
        ...s,
        filter: JSON.stringify(s.filter),
      }));
  }
  return doc;
}

function getCommitData(file) {
  const [base, ...localPath] = file.split(path.sep).slice(1);
  const stdout = execSync(
    `cd ${base} && git log "${[".", ...localPath].join(path.sep)}"`,
    { encoding: "utf8" }
  );
  const [, hash, author, date] = /commit (\w+)\nAuthor: (.*)\nDate: (.*)/.exec(
    stdout
  );
  return {
    hash,
    author: author.trim(),
    date: new Date(date.trim().toString()),
  };
}

function collectVisualizationFolder(app, path, source, dashboards, folderName) {
  const visPath = `${path}/${folderName}`;
  const exists = fs.existsSync(visPath);
  if (!exists) return [];
  const visualizations = fs.readdirSync(visPath);
  return visualizations
    .map((vis) => ({
      doc: JSON.parse(
        fs.readFileSync(`${visPath}/${vis}`, { encoding: "utf8" })
      ),
      path: `${visPath}/${vis}`,
      commit: getCommitData(`${visPath}/${vis}`),
    }))
    .map(({ doc, path, commit }) => ({
      doc: cleanupDoc(doc),
      soType: folderName,
      app,
      source,
      link: "by_reference",
      dashboard: dashboards.get(doc.id),
      path,
      commit,
    }));
}

function collectDashboardFolder(app, path, source) {
  const dashboardPath = `${path}/dashboard`;
  const exists = fs.existsSync(dashboardPath);
  if (!exists) return { visualizations: [], dashboards: [] };
  const dashboards = fs.readdirSync(dashboardPath);
  const dashboardMap = new Map();
  const visualizations = [];

  dashboards.forEach((d) => {
    const dashboard = JSON.parse(
      fs.readFileSync(`${dashboardPath}/${d}`, { encoding: "utf8" })
    );
    const commit = getCommitData(`${dashboardPath}/${d}`);
    dashboard.references.forEach((r) => {
      dashboardMap.set(r.id, dashboard.attributes.title);
    });
    (typeof dashboard.attributes.panelsJSON === "string"
      ? JSON.parse(dashboard.attributes.panelsJSON)
      : dashboard.attributes.panelsJSON
    )
      .filter(
        (panel) =>
          (panel.type === "visualization" && panel.embeddableConfig.savedVis) ||
          (panel.type === "lens" && panel.embeddableConfig.attributes) ||
          (panel.type === "map" && panel.embeddableConfig.attributes)
      )
      .map((p) => {
        visualizations.push({
          doc: p,
          soType: p.type,
          app,
          source,
          link: "by_value",
          dashboard: dashboard.attributes.title,
          path: `${dashboardPath}/${d}`,
          commit,
        });
      });
  });

  return {
    visualizations,
    dashboards: dashboardMap,
  };
}

function collectIntegrations() {
  const allVis = [];
  const packages = fs.readdirSync("./integrations/packages");
  packages.forEach((package) => {
    const { visualizations, dashboards } = collectDashboardFolder(
      package,
      `./integrations/packages/${package}/kibana`,
      "integration"
    );
    const manifest = YAML.parse(
      fs.readFileSync(`./integrations/packages/${package}/manifest.yml`, {
        encoding: "utf8",
      })
    );
    visualizations.push(
      ...collectVisualizationFolder(
        package,
        `./integrations/packages/${package}/kibana`,
        "integration",
        dashboards,
        "visualization"
      )
    );
    visualizations.push(
      ...collectVisualizationFolder(
        package,
        `./integrations/packages/${package}/kibana`,
        "integration",
        dashboards,
        "lens"
      )
    );
    visualizations.push(
      ...collectVisualizationFolder(
        package,
        `./integrations/packages/${package}/kibana`,
        "integration",
        dashboards,
        "map"
      )
    );
    visualizations.push(
      ...collectVisualizationFolder(
        package,
        `./integrations/packages/${package}/kibana`,
        "integration",
        dashboards,
        "search"
      )
    );
    console.log(`Collected ${visualizations.length} vis in ${package}`);
    allVis.push(...visualizations.map((v) => ({ ...v, manifest })));
  });
  return allVis;
}

function collectBeats() {
  const allVis = [];
  function recurse(root) {
    const list = fs.readdirSync(root);
    list.forEach((l) => {
      if (l === "7") {
        const path = `${root}/7`;
        const { visualizations, dashboards } = collectDashboardFolder(
          root,
          path,
          "beat"
        );
        visualizations.push(
          ...collectVisualizationFolder(
            root,
            path,
            "beat",
            dashboards,
            "visualization"
          )
        );
        visualizations.push(
          ...collectVisualizationFolder(root, path, "beat", dashboards, "lens")
        );
        visualizations.push(
          ...collectVisualizationFolder(root, path, "beat", dashboards, "map")
        );
        console.log(`Collected ${visualizations.length} vis in ${root}`);
        allVis.push(...visualizations);
        return;
      }
      if (fs.statSync(`${root}/${l}`).isDirectory()) {
        recurse(`${root}/${l}`);
      }
    });
  }
  recurse("./beats");
  return allVis;
}

(async function () {
  const vis = [...collectIntegrations(), ...collectBeats()];

  if (fs.existsSync("./result.json")) {
    fs.rmSync("./result.json");
  }
  fs.writeFileSync("./result.json", JSON.stringify(vis, null, 2));

  console.log(`uploading ${vis.length} visualizations...`);
  await client.indices.delete({
    index: "legacy_vis",
    allow_no_indices: true,
    ignore_unavailable: true,
  });
  await client.indices.create({
    index: "legacy_vis",
    mappings: {
      properties: {
        doc: {
          type: "flattened",
          depth_limit: 50,
        },
        manifest: {
          type: "flattened",
        },
        soType: {
          type: "keyword",
        },
        app: {
          type: "keyword",
        },
        source: {
          type: "keyword",
        },
        link: {
          type: "keyword",
        },
        dashboard: {
          type: "keyword",
        },
        path: {
          type: "keyword",
        },
        commit: {
          properties: {
            hash: {
              type: "keyword",
            },
            author: {
              type: "keyword",
            },
            date: {
              type: "date",
            },
          },
        },
      },
    },
  });

  const chunkSize = 250;
  for (let i = 0; i < vis.length; i += chunkSize) {
    console.log(i);
    const chunk = vis.slice(i, i + chunkSize);
    const response = await client.bulk({
      operations: chunk.flatMap((v) => [
        {
          index: {
            _index: "legacy_vis",
            _id: crypto.randomBytes(16).toString("hex"),
          },
        },
        v,
      ]),
    });
    if (response.errors) {
      console.log(JSON.stringify(response, null, 2));
      throw new Error();
    }
  }
  await client.indices.refresh({ index: "legacy_vis" });
  console.log("done");
})();
