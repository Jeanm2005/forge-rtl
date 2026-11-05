import { readFileSync, readdirSync } from 'node:fs';
const dir = process.argv[2];
const negative = process.argv[3] === 'negative';
const matched = readFileSync(`${dir}/matched.ids`, 'utf8').trim().split('\n')
  .filter(line => line.trim() && !line.startsWith('#'))
  .map(line => line.trim().split(/\s+/));
const allowed = new Set(['clk', 'rst', 'en', 'a', 'b', 'acc']);
if (!matched.some(([, gold, gate]) => gold === 'acc' && gate === 'acc') ||
    matched.some(([module, gold, gate]) => module !== 'mac_formal' ||
      gold !== gate || !allowed.has(gold))) {
  throw new Error(`Unexpected EQY matching: ${JSON.stringify(matched)}`);
}
const partitions = readdirSync(`${dir}/strategies`, { withFileTypes: true })
  .filter(entry => entry.isDirectory());
if (!partitions.length) throw new Error('No proven partitions');
let failed = 0;
for (const partition of partitions) {
  const status = readFileSync(`${dir}/strategies/${partition.name}/induction/status`, 'utf8');
  const outcome = status.trim().split(/\s+/)[0];
  if (outcome === 'FAIL') failed++;
  if (outcome !== 'PASS' && !(negative && outcome === 'FAIL')) {
    throw new Error(`Partition ${partition.name} did not complete proof: ${status}`);
  }
}
if (negative && failed === 0) throw new Error('No confirmed failing partition');
console.log(`PASS EQY matching audit: public ports only, ${partitions.length} completed partitions, ${failed} counterexamples`);
