import { BridgeClient, PendingRequestError, ProtocolError, type PreparedRequest, type RpcResult } from './client.js';
import { object, type JsonObject } from './json.js';

export interface LeaseIdentity {
  taskId: string;
  assignmentId: string;
  generation: number;
  token: string;
  agentId: string;
}

/** Serializes this client's renew/report/submit CAS writes; never asserts external exclusivity. */
export class LeaseSession {
  private tail: Promise<void> = Promise.resolve();
  private unresolved: PreparedRequest | undefined;
  private task: JsonObject | undefined;
  private runId: string | undefined;
  private leaseExpiresAt: number | undefined;

  constructor(readonly client: BridgeClient, readonly identity: LeaseIdentity) {
    client.validator.validate('Lease', { assignment_id: identity.assignmentId, generation: identity.generation, token: identity.token });
    client.validator.validate('Id', identity.taskId);
    client.validator.validate('Id', identity.agentId);
  }

  static fromClaim(client: BridgeClient, result: RpcResult): LeaseSession {
    const data = object(result.data); const task = object(data.task!);
    const records = data.records;
    if (!Array.isArray(records) || typeof data.lease_token !== 'string') throw new ProtocolError('Missing private claim result');
    const assignment = records.map(object).find(record => record.record_type === 'Assignment');
    if (!assignment) throw new ProtocolError('Claim result has no Assignment');
    const record = object(assignment.record!);
    const session = new LeaseSession(client, { taskId: task.task_id as string, assignmentId: record.assignment_id as string, generation: record.generation as number, token: data.lease_token, agentId: record.agent_id as string });
    session.task = task;
    session.leaseExpiresAt = Date.parse(record.lease_expires_at as string);
    return session;
  }

  private serial<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.tail.then(operation);
    this.tail = result.then(() => {}, () => {});
    return result;
  }

  private async refresh(): Promise<void> {
    const result = await this.client.call('task.get', { task_id: this.identity.taskId, as_agent_id: this.identity.agentId });
    const task = object(result.data);
    if (task.current_assignment_id !== this.identity.assignmentId || task.generation !== this.identity.generation || !['claimed', 'active'].includes(task.state as string)) throw new ProtocolError('Assignment is no longer current');
    this.task = task;
  }

  private params(extra: JsonObject): JsonObject {
    if (!this.task) throw new ProtocolError('No current task revision');
    return { ...extra, task_id: this.identity.taskId, expected_revision: this.task.revision!, as_agent_id: this.identity.agentId,
      lease: { assignment_id: this.identity.assignmentId, generation: this.identity.generation, token: this.identity.token } };
  }

  private absorb(result: RpcResult): void {
    const data = object(result.data); this.task = object(data.task!);
    if (Array.isArray(data.records)) {
      for (const wrapper of data.records.map(object)) {
        if (wrapper.record_type === 'Assignment') {
          const assignment = object(wrapper.record!);
          if (assignment.assignment_id === this.identity.assignmentId) this.leaseExpiresAt = Date.parse(assignment.lease_expires_at as string);
        }
        if (wrapper.record_type === 'Run') this.runId = object(wrapper.record!).run_id as string;
      }
    }
  }

  private async write(method: string, extra: JsonObject): Promise<RpcResult> {
    if (this.unresolved) throw new PendingRequestError();
    await this.refresh();
    const request = await this.client.prepare(method, this.params(extra));
    this.unresolved = request;
    try {
      const result = await this.client.execute(request);
      this.absorb(result); this.unresolved = undefined; return result;
    } catch (error) {
      if (!this.client.requestState(request.key)?.unknown) this.unresolved = undefined;
      throw error;
    }
  }

  start(): Promise<RpcResult> { return this.serial(() => this.write('run.start', {})); }
  renew(leaseSeconds: number): Promise<RpcResult> { return this.serial(() => this.write('assignment.renew', { lease_seconds: leaseSeconds })); }
  report(runId: string, message: string, evidenceRefs: JsonObject[] = []): Promise<RpcResult> {
    return this.serial(() => this.write('run.report', { run_id: runId, message, evidence_refs: evidenceRefs, extensions: {} }));
  }
  submit(runId: string, delivery: JsonObject): Promise<RpcResult> {
    return this.serial(() => this.write('run.submit', { ...delivery, run_id: runId }));
  }

  retryPending(): Promise<RpcResult> {
    return this.serial(async () => {
      if (!this.unresolved) throw new ProtocolError('No unresolved lease request');
      const result = await this.client.execute(this.unresolved);
      this.absorb(result); this.unresolved = undefined; return result;
    });
  }

  /** A current fenced write is required before resuming external work after recovery. */
  resume(runId?: string): Promise<RpcResult> {
    return this.serial(async () => {
      if (this.unresolved) throw new PendingRequestError();
      await this.refresh();
      if (this.task!.state === 'claimed') return this.write('run.start', {});
      const currentRun = runId ?? this.runId;
      if (!currentRun) throw new ProtocolError('Supply the current run ID to resume an active Assignment');
      return this.write('run.report', { run_id: currentRun, message: 'Client recovered; checking current execution authority', evidence_refs: [], extensions: {} });
    });
  }

  supervise(options: { leaseSeconds: number; intervalMs: number; safetyMarginMs?: number; onError: (error: unknown) => void }): () => void {
    if (!Number.isSafeInteger(options.intervalMs) || options.intervalMs < 1) throw new ProtocolError('Invalid supervision interval');
    const margin = options.safetyMarginMs ?? 1000;
    if (!Number.isSafeInteger(margin) || margin < 1) throw new ProtocolError('Invalid lease safety margin');
    let stopped = false; let running = false;
    let timer: ReturnType<typeof setInterval> | undefined;
    let watchdog: ReturnType<typeof setTimeout> | undefined;
    let safetyDeadline = 0;
    const stop = (): void => { stopped = true; clearInterval(timer); clearTimeout(watchdog); };
    const fail = (error: unknown): void => { if (!stopped) { stop(); options.onError(error); } };
    const arm = (): void => {
      if (stopped) return;
      clearTimeout(watchdog);
      const remaining = (this.leaseExpiresAt ?? NaN) - Date.now() - margin;
      if (!Number.isFinite(remaining) || remaining <= 0) {
        fail(new ProtocolError('No confirmed lease time remains before the safety deadline')); return;
      }
      // The independent timer remains armed while renew waits for transport/journal/CAS.
      safetyDeadline = performance.now() + remaining;
      watchdog = setTimeout(() => fail(new ProtocolError('Confirmed lease safety deadline reached')), remaining);
      watchdog.unref();
    };
    arm();
    if (!stopped) {
      timer = setInterval(() => {
        if (stopped || running) return;
        running = true;
        void this.renew(options.leaseSeconds).then(() => {
          if (stopped) return;
          if (performance.now() >= safetyDeadline) fail(new ProtocolError('Renewal arrived after the confirmed lease safety deadline'));
          else arm();
        })
          .catch(fail).finally(() => { running = false; });
      }, options.intervalMs);
      timer.unref();
    }
    // A late successful response cannot re-arm a stopped supervisor.
    return stop;
  }
}
