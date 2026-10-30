import { mkdir, open, readFile, link, chmod, unlink, readdir, access } from 'node:fs/promises';
import { join } from 'node:path';
import { randomUUID } from 'node:crypto';
import { canonical, object, parseExact, type JsonObject } from './json.js';

export interface JournalEntry {
  endpoint: string;
  method: string;
  params: JsonObject;
  digest: string;
  unknown: boolean;
  attempts: number;
  attemptId?: string;
  confirmed?: boolean;
}

export interface RequestJournal {
  load(key: string): Promise<JournalEntry | undefined>;
  save(key: string, entry: JournalEntry): Promise<void>;
}

/** Private local recovery data; includes lease credentials but never bearer credentials. */
export class FileJournal implements RequestJournal {
  constructor(private readonly directory: string) {}

  private filename(key: string): string {
    if (!/^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/.test(key)) throw new Error('Invalid journal key');
    return join(this.directory, key + '.json');
  }

  async load(key: string): Promise<JournalEntry | undefined> {
    const filename = this.filename(key);
    let value;
    try { value = object(parseExact(await readFile(filename))); }
    catch (error) { if ((error as NodeJS.ErrnoException).code === 'ENOENT') return undefined; throw error; }
    if ('unknown' in value || 'attempts' in value) throw new Error('Legacy mutable journal requires explicit recovery; refusing to discard uncertainty');
    if (typeof value.endpoint !== 'string' || typeof value.method !== 'string' || typeof value.digest !== 'string') throw new Error('Invalid recovery journal');
    let attempts: string[] = [];
    try { attempts = await readdir(filename + '.attempts'); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
    let confirmed = false;
    try { await access(filename + '.confirmed'); confirmed = true; }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'ENOENT') throw error; }
    return { endpoint: value.endpoint, method: value.method, params: object(value.params!), digest: value.digest,
      unknown: attempts.length > 0 && !confirmed, attempts: attempts.length, confirmed };
  }

  private async publish(filename: string, contents: string): Promise<void> {
    const temporary = filename + '.' + randomUUID() + '.tmp';
    const file = await open(temporary, 'wx', 0o600);
    try {
      await file.writeFile(contents, 'utf8'); await file.sync();
    } finally { await file.close(); }
    try { await link(temporary, filename); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== 'EEXIST') throw error; }
    finally { await unlink(temporary); }
  }

  private async syncDirectory(path: string): Promise<void> {
    const directory = await open(path, 'r');
    try { await directory.sync(); } finally { await directory.close(); }
  }

  async save(key: string, entry: JournalEntry): Promise<void> {
    const filename = this.filename(key);
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    await chmod(this.directory, 0o700);
    // Immutable request identity is published with link(O_EXCL semantics), never rename-overwritten.
    const request = canonical({ endpoint: entry.endpoint, method: entry.method, params: entry.params, digest: entry.digest });
    await this.publish(filename, request);
    if (await readFile(filename, 'utf8') !== request) throw new Error('Journal key already belongs to a different immutable request');
    if (entry.unknown) {
      if (!entry.attemptId || !/^[0-9a-f-]{36}$/.test(entry.attemptId)) throw new Error('Missing journal attempt identity');
      const attempts = filename + '.attempts';
      await mkdir(attempts, { recursive: true, mode: 0o700 });
      await this.publish(join(attempts, entry.attemptId), 'sent');
      await this.syncDirectory(attempts);
    }
    // A rejection can never remove another instance's durable uncertainty. Only a
    // validated success receipt creates the monotonic confirmation marker.
    if (entry.confirmed) await this.publish(filename + '.confirmed', 'confirmed');
    await this.syncDirectory(this.directory);
  }
}
