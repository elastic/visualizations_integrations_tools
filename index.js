const crypto = require("crypto");
const { collectIntegrations, collectBeats } = require("./collect_functions.js");

const fs = require("fs");
const { Client } = require("@elastic/elasticsearch");
const client = new Client({
  node: process.env.ES || "http://elastic:changeme@localhost:9200",
});

(async function () {
  //const vis = [...collectIntegrations(), ...collectBeats()];
  const vis = [...collectIntegrations()];

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

module.exports = { collectIntegrations };
