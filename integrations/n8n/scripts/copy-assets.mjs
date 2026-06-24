// Place the takuhai icon next to each compiled node AND credential. The canonical artwork
// is the repo brand asset docs/assets/logo-face.svg (single source of truth) — tsc emits
// only JS/d.ts, so the icon is copied in here. n8n resolves a node's
// `icon: 'file:takuhai.svg'` relative to its own dist/nodes/<Node>/ dir, and a credential's
// relative to dist/credentials/, so the same file is dropped in each place. The path is
// repo-relative and identical for a local build (cwd = integrations/n8n) and the Docker
// build (WORKDIR mirrors the repo layout).
import { access, cp, readdir } from 'node:fs/promises';
import { join } from 'node:path';

const ICON = '../../docs/assets/logo-face.svg';
const NODES = 'dist/nodes';
const CREDENTIALS = 'dist/credentials';

// Fail loudly rather than shipping iconless nodes if the brand asset is out of scope.
await access(ICON);

let n = 0;
for (const entry of await readdir(NODES, { withFileTypes: true })) {
	if (!entry.isDirectory()) continue;
	await cp(ICON, join(NODES, entry.name, 'takuhai.svg'));
	n++;
}

// Credentials are flat .js files in one dir; a single icon there serves any credential
// referencing `file:takuhai.svg`.
await cp(ICON, join(CREDENTIALS, 'takuhai.svg'));
n++;

console.log(`copy-assets: placed takuhai.svg in ${n} node/credential dir(s) from ${ICON}`);
