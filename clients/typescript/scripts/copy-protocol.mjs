import { copyFile, mkdir } from 'node:fs/promises';
const destination = new URL('../dist/protocol/', import.meta.url);
await mkdir(destination, { recursive: true });
for (const name of ['schema.json', 'methods.json']) {
  await copyFile(new URL(`../../../protocol/m1/${name}`, import.meta.url), new URL(name, destination));
}
