import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { BACKOFF_MAX_MS, ReconnectPolicy, backoffDelay } from "../src/backoff";

describe("backoff", () => {
  it("doubles from 1s and caps at 30s", () => {
    assert.deepEqual([1, 2, 3, 4, 5, 6, 7, 20].map(backoffDelay), [1000, 2000, 4000, 8000, 16000, 30000, 30000, 30000]);
    assert.equal(backoffDelay(0), 1000);
    assert.equal(backoffDelay(100), BACKOFF_MAX_MS);
  });

  it("resets after a healthy connection", () => {
    const policy = new ReconnectPolicy();
    assert.equal(policy.nextDelay(), 1000);
    assert.equal(policy.nextDelay(), 2000);
    assert.equal(policy.nextDelay(), 4000);
    assert.equal(policy.attempts, 3);
    policy.reset();
    assert.equal(policy.attempts, 0);
    assert.equal(policy.nextDelay(), 1000);
  });
});
