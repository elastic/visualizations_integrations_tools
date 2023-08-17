const { collectIntegrations } = require("./collect_functions.js");
const { execSync } = require("child_process");
const { groupBy } = require("lodash");

const START_COMMIT = "63a3b0cac5d0a61a309240595adaaf01a07cd19a";
const END_COMMIT = "df3598bac6683c88978a7426a4724592032bad53";

const getLegacyVisCountsByApp = (allPanels) => {
  const groups = groupBy(
    allPanels.filter((v) => v.soType === "visualization"),
    (v) => v.app
  );
  const counts = {};
  for (const [name, group] of Object.entries(groups)) {
    counts[name] = group.length;
  }

  return counts;
};

execSync(`cd ./integrations && git checkout ${START_COMMIT}`);
const before = collectIntegrations();
// writeFileSync("./before.json", JSON.stringify(before, null, 2));
const beforeCounts = getLegacyVisCountsByApp(before);
console.log(beforeCounts);

execSync(`cd ./integrations && git checkout ${END_COMMIT}`);
const after = collectIntegrations();
// writeFileSync("./after.json", JSON.stringify(before, null, 2));
const afterCounts = getLegacyVisCountsByApp(after);

const differences = Object.entries(beforeCounts)
  .map(([name, beforeCount]) => {
    const afterCount = afterCounts[name];
    if (beforeCount !== afterCount) {
      return {
        name,
        beforeCount,
        afterCount,
      };
    }
  })
  .filter(Boolean);

console.log(differences);
