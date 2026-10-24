export const BACKOFF_MIN_MS = 1000;
export const BACKOFF_MAX_MS = 30000;
export const HEARTBEAT_TIMEOUT_MS = 90000;

/** Delay before the n-th consecutive restart attempt (1-based): 1s, 2s, 4s ... capped at 30s. */
export function backoffDelay(attempt: number): number {
  const n = Math.max(1, Math.floor(attempt));
  const delay = BACKOFF_MIN_MS * 2 ** (n - 1);
  return Math.min(delay, BACKOFF_MAX_MS);
}

export class ReconnectPolicy {
  private failures = 0;

  get attempts(): number {
    return this.failures;
  }

  /** Records a failure and returns how long to wait before trying again. */
  nextDelay(): number {
    this.failures += 1;
    return backoffDelay(this.failures);
  }

  reset(): void {
    this.failures = 0;
  }
}
