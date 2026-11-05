import { readFileSync } from 'node:fs';
const results = {};
for (const variant of ['gold', 'candidate']) {
  const top = `mac_${variant}`;
  const netlist = JSON.parse(readFileSync(`build/synth/${variant}/netlist.json`));
  const modules = Object.keys(netlist.modules);
  if (modules.length !== 1 || modules[0] !== top ||
      Number(netlist.modules[top].attributes.top) !== 1) {
    throw new Error(`Unexpected synthesized top: ${modules}`);
  }
  const stats = JSON.parse(readFileSync(`build/synth/${variant}/stat.json`));
  const module = stats.modules[`\\${top}`];
  if (!module || !Number.isInteger(module.num_cells)) throw new Error('Missing statistics');
  const types = {};
  for (const cell of Object.values(netlist.modules[top].cells)) {
    types[cell.type] = (types[cell.type] ?? 0) + 1;
  }
  if (Object.values(types).reduce((a, b) => a + b, 0) !== module.num_cells ||
      JSON.stringify(Object.entries(types).sort()) !==
      JSON.stringify(Object.entries(module.num_cells_by_type).sort())) {
    throw new Error('Netlist/statistics counts disagree');
  }
  results[variant] = { total_cells: module.num_cells, cells_by_type: types };
}
const allTypes = new Set([...Object.keys(results.gold.cells_by_type),
                          ...Object.keys(results.candidate.cells_by_type)]);
results.delta = {
  total_cells: results.candidate.total_cells - results.gold.total_cells,
  cells_by_type: Object.fromEntries([...allTypes].sort().map(type =>
    [type, (results.candidate.cells_by_type[type] ?? 0) - (results.gold.cells_by_type[type] ?? 0)])),
};
console.log(JSON.stringify(results, null, 2));
