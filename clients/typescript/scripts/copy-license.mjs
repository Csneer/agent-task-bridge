import { copyFile } from 'node:fs/promises';
try {
  await copyFile(new URL('../../../LICENSE', import.meta.url), new URL('../LICENSE', import.meta.url));
} catch {
  throw new Error('Packing this SDK requires the repository root LICENSE. Use the complete release checkout.');
}
