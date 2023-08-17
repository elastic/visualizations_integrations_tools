#!/usr/bin/env node

const { collectIntegrations } = require("./collect_functions.js");

const LEGACY_VIS_LIMIT = 296;

const allContent = collectIntegrations(".");

const allLegacyVis = allContent.filter((v) => v.soType === "visualization");

if (allLegacyVis.length > LEGACY_VIS_LIMIT) {
  process.exit(1);
} else {
  process.exit(0);
}
