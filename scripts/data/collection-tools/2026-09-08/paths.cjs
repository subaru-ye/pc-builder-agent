const path = require('path');
const ROOT = path.resolve(__dirname, '../../../..');
const DATA = path.resolve(process.env.COLLECTION_DATA_DIR || path.join(ROOT, 'var/data/collections/2026-09-08'));
const OUTPUT = path.resolve(process.env.COLLECTION_OUTPUT_DIR || path.join(DATA, 'rebuild'));
module.exports = { ROOT, DATA, OUTPUT };
