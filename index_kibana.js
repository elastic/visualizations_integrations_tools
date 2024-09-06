const crypto = require("crypto");
const glob = require("glob");
const execSync = require("child_process").execSync;
const path = require("path");
const YAML = require("yaml");

const fs = require("fs");
const { Client } = require("@elastic/elasticsearch");
const client = new Client({
  node: process.env.ES || "http://elastic:changeme@localhost:9200",
});

const pluginName = (n) =>
  /\/plugins\/(.*?)((\/common)|(\/public)|(\/server)).*/.exec(n)?.[1];

function getCommitData() {
  const stdout = execSync(`cd kibana && git log -1`, { encoding: "utf8" });
  const [, hash, author, date] = /commit (\w+)\nAuthor: (.*)\nDate: (.*)/.exec(
    stdout
  );
  return {
    hash,
    author: author.trim(),
    date: new Date(date.trim().toString()),
  };
}

function groupBy(list, groupFn) {
  const map = new Map();
  list.forEach((i) => {
    const group = groupFn(i);
    // exclude lens itself and invalid entries
   if (group === 'lens' || !i || !group ) return;
    if (!map.has(group)) {
      map.set(group, []);
    }
    map.get(group).push(i);
  });
  return map;
}

function getUsage(commit, searchTerm, usage) {
  let srcFiles = [];
  try {
    srcFiles = execSync(`cd kibana && ag "${searchTerm}" -l ./src/plugins`)
      .toString("utf8")
      .split("\n");
  } catch (e) {}
  let xpackFiles = [];
  try {
    xpackFiles = execSync(`cd kibana && ag "${searchTerm}" -l ./x-pack/plugins`)
      .toString("utf8")
      .split("\n");
  } catch (e) {}
  const srcGroups = groupBy(srcFiles, pluginName);
  const xpackGroups = groupBy(xpackFiles, pluginName);
  const srcUsages = [...srcGroups.entries()].map(([id, files]) => ({
    commit,
    usage,
    name: id,
    files,
    occurences: files.length,
  }));
  const xpackUsages = [...xpackGroups.entries()].map(([id, files]) => ({
    commit,
    usage,
    name: `x-pack/${id}`,
    files,
    occurences: files.length,
  }));
  return [...srcUsages, ...xpackUsages];
}

/*
Document structure in usages index:
Per crawling per pacakge/plugin
{
  date: 2022-08-11...,
  usage: elastic/charts | lens-plugin | ExploratoryViewEmbeddable
  name: x-pack/lens | vis_types/timelion,
  files: [ kibana/src/..., kibana/src/...],
  occurences: files.length
}

elastic/charts: Something is imported from the elastic/charts package
lens: Something is imported from the lens plugin
exploratory_view: The ExploratoryViewEmbeddable component is used somewhere
*/
function collectUsages() {
  const commitData = getCommitData();

  return [
    ...getUsage(commitData, "elastic/charts", 'elastic-charts'),
    ...getUsage(commitData, "lens-plugin", 'lens'),
    ...getUsage(commitData, "/lens/", 'lens'),
    ...getUsage(commitData, "ExploratoryViewEmbeddable", 'exploratory-view'),
  ];
}

(async function () {
  const usages = collectUsages();
  if (fs.existsSync("./result.json")) {
    fs.rmSync("./result.json");
  }
  fs.writeFileSync("./result.json", JSON.stringify(usages, null, 2));

  console.log(`uploading ${usages.length} usages...`);
  const exists = await client.indices.exists({
    index: "usages",
  });
  if (!exists) {
    await client.indices.create({
      index: "usages",
      mappings: {
        properties: {
          occurences: {
            type: "long",
          },
          files: {
            type: "keyword",
          },
          name: {
            type: "keyword",
          },
          usage: {
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
  }

  const chunkSize = 250;
  for (let i = 0; i < usages.length; i += chunkSize) {
    console.log(`uploading #${i+1} out of ${Math.ceil(usages.length / chunkSize)}`);
    const chunk = usages.slice(i, i + chunkSize);
    const response = await client.bulk({
      operations: chunk.flatMap((v) => [
        {
          index: {
            _index: "usages",
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
  await client.indices.refresh({ index: "usages" });
  console.log("done");
})();
