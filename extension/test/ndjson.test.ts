import assert from "node:assert/strict";
import { describe, it } from "node:test";
import { LineSplitter, errorOf, parseJsonLine, parseJsonLines } from "../src/ndjson";

describe("LineSplitter", () => {
  it("joins partial chunks into whole lines", () => {
    const s = new LineSplitter();
    assert.deepEqual(s.push('{"a":1}\n{"b"'), ['{"a":1}']);
    assert.deepEqual(s.push(':2}\r\n\n{"c":3}'), ['{"b":2}']);
    assert.equal(s.flush(), '{"c":3}');
    assert.equal(s.flush(), undefined);
  });

  it("drops blank lines", () => {
    const s = new LineSplitter();
    assert.deepEqual(s.push("\n\n  \n"), ["  "]);
  });
});

describe("parseJsonLine", () => {
  it("parses objects and ignores plain text", () => {
    assert.deepEqual(parseJsonLine('{"kind":"heartbeat"}'), { kind: "heartbeat" });
    assert.equal(parseJsonLine("pawprints dev (unknown)"), undefined);
    assert.equal(parseJsonLine("{not json"), undefined);
    assert.equal(parseJsonLine(""), undefined);
  });

  it("parses several lines, skipping junk", () => {
    const values = parseJsonLines('{"id":"a"}\nwarning: x\n{"id":"b"}\n');
    assert.deepEqual(values, [{ id: "a" }, { id: "b" }]);
  });
});

describe("errorOf", () => {
  it("recognises the binary's error shape", () => {
    assert.deepEqual(errorOf({ error: "no event matches", exit: 1 }), { message: "no event matches", exit: 1 });
    assert.deepEqual(errorOf({ error: "changed since", exit: 3 }), { message: "changed since", exit: 3 });
  });

  it("defaults exit to 1 and ignores other objects", () => {
    assert.deepEqual(errorOf({ error: "x" }), { message: "x", exit: 1 });
    assert.equal(errorOf({ id: "01JB", error: 5 }), undefined);
    assert.equal(errorOf("error"), undefined);
    assert.equal(errorOf(null), undefined);
  });
});
