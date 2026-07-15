// Минимальный безопасный JSON-extractor: читает JSON из stdin, путь — argv[2].
// Поддерживает точку и [index]. Пример: jp.js 'token'  |  jp.js 'commands[0].id'
const fs = require('fs');
let raw = '';
try { raw = fs.readFileSync(0, 'utf8'); } catch { process.exit(1); }
let d;
try { d = JSON.parse(raw); } catch { process.exit(1); }
const path = process.argv[2] || '';
let cur = d;
for (const tok of path.match(/[^.\[\]]+|\[\d+\]/g) || []) {
  if (tok.startsWith('[')) cur = cur[parseInt(tok.slice(1, -1), 10)];
  else cur = cur ? cur[tok] : undefined;
}
console.log(cur === undefined ? '' : cur);
